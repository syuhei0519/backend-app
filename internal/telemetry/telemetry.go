// OTel SDKとOTLP送信を所有。cmd/server/main.goが初期化し、HTTP/DB処理が計測する。
// 送信先設定はapplication-manifest/charts/backend/templates/_helpers.tpl、受信側はplatform-gitops/charts/otel-collector/templates/configmap.yaml。
// Package telemetryは、有限の送信待機と安全な属性を持つ非同期OTLP計装を担当する。
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// 秒単位histogramのbucket境界。docs/telemetry-contract.mdと設計§7.5で固定。
// p95はbucketによる推定であり、すべての要求の正確な95百分位を保存するものではない。
var DurationBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type Telemetry struct {
	Tracer            trace.Tracer
	Propagator        propagation.TextMapPropagator
	Resource          *resource.Resource
	Instance, Version string
	requests          metric.Int64Counter
	duration          metric.Float64Histogram
	mp                *sdkmetric.MeterProvider
	tp                *sdktrace.TracerProvider
	exportErrors      atomic.Int64
}

// Newはproviderを注入でき、本番と同じ計測器をメモリ内readerでテストできる。
func New(mp metric.MeterProvider, tp trace.TracerProvider, res *resource.Resource, instance, version string) (*Telemetry, error) {
	meter := mp.Meter("account-backend")
	requests, err := meter.Int64Counter("pe_http_server_requests", metric.WithUnit("1"))
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("pe_http_server_request_duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(DurationBounds...))
	if err != nil {
		return nil, err
	}
	return &Telemetry{Tracer: tp.Tracer("account-backend"), Propagator: propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}), Resource: res, Instance: instance, Version: version, requests: requests, duration: duration}, nil
}

// プロセス起動ごとにinstance UUIDを作る。Pod UIDとは別なので同Pod内再起動でも区別できる。
func Init(ctx context.Context, version string, logger *slog.Logger) (*Telemetry, error) {
	instance := uuid.NewString()
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Host != "otel-collector.observability.svc.cluster.local:4318" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
			return nil, fmt.Errorf("invalid Collector endpoint")
		}
		if !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(version) || os.Getenv("POD_NAMESPACE") != "account" || os.Getenv("DEPLOYMENT_ENVIRONMENT") != "local" {
			return nil, fmt.Errorf("invalid deployment telemetry identity")
		}
	}
	// Resource属性は「誰が計測したか」を示す。versionはbuild時のsource SHA、instanceはプロセス寿命中固定。
	// Collectorはname/version/instanceを保持し、全属性をすべての時系列ラベルへ複製しない。
	res := resource.NewSchemaless(attribute.String("service.name", "account-backend"), attribute.String("service.version", version), attribute.String("service.instance.id", instance), attribute.String("deployment.environment.name", os.Getenv("DEPLOYMENT_ENVIRONMENT")), attribute.String("k8s.namespace.name", os.Getenv("POD_NAMESPACE")), attribute.String("k8s.pod.uid", os.Getenv("POD_UID")))
	var readers []sdkmetric.Option
	var traceOptions []sdktrace.TracerProviderOption
	if endpoint != "" {
		// SDK→CollectorのHTTP送信は3秒上限・再試行なし。メトリクスは累積値を10秒ごとに送る。
		// 後段Prometheusのscrape30秒とは別の周期。観測先停止はCRUDを同期的に待たせない設計。
		me, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint), otlpmetrichttp.WithURLPath("/v1/metrics"), otlpmetrichttp.WithTimeout(3*time.Second), otlpmetrichttp.WithHeaders(map[string]string{}), otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{Enabled: false}), otlpmetrichttp.WithTemporalitySelector(func(sdkmetric.InstrumentKind) metricdata.Temporality { return metricdata.CumulativeTemporality }))
		if err != nil {
			return nil, err
		}
		readers = append(readers, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(me, sdkmetric.WithInterval(10*time.Second), sdkmetric.WithTimeout(3*time.Second))))
		te, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint), otlptracehttp.WithURLPath("/v1/traces"), otlptracehttp.WithTimeout(3*time.Second), otlptracehttp.WithHeaders(map[string]string{}), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
		if err != nil {
			_ = me.Shutdown(ctx)
			return nil, err
		}
		// spanは非同期batchへ渡す。queue256、batch最大64、待機1秒、export3秒の有限設定。
		// 混雑時や送信障害ではspan欠損を許容し、全span保存を保証しない。
		traceOptions = append(traceOptions, sdktrace.WithBatcher(te, sdktrace.WithMaxQueueSize(256), sdktrace.WithMaxExportBatchSize(64), sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(3*time.Second)))
	}
	readers = append(readers, sdkmetric.WithResource(res), sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Name: "pe_http_server_request_duration"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: DurationBounds}})))
	mp := sdkmetric.NewMeterProvider(readers...)
	// 親のsampling判断を継承し、親がない要求は採用する。AlwaysSampleでも送信・保存の成功保証ではない。
	traceOptions = append(traceOptions, sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())))
	tp := sdktrace.NewTracerProvider(traceOptions...)
	t, err := New(mp, tp, res, instance, version)
	if err != nil {
		return nil, errors.Join(err, mp.Shutdown(ctx), tp.Shutdown(ctx))
	}
	t.mp, t.tp = mp, tp
	// 送信エラーは安全な固定ログとcounterで観測する。再送失敗件数や失われたspan数そのものとは同じでない。
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {
		t.exportErrors.Add(1)
		logger.Warn("telemetry export failed", "service", "account-backend")
	}))
	meter := mp.Meter("account-backend")
	goroutines, err := meter.Int64ObservableGauge("pe_go_goroutines", metric.WithUnit("{goroutine}"))
	if err != nil {
		return nil, err
	}
	alloc, err := meter.Int64ObservableGauge("pe_go_memory_alloc", metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	failures, err := meter.Int64ObservableCounter("pe_telemetry_export_errors", metric.WithUnit("{error}"))
	if err != nil {
		return nil, err
	}
	_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		observer.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
		observer.ObserveInt64(alloc, int64(stats.Alloc))
		observer.ObserveInt64(failures, t.exportErrors.Load())
		return nil
	}, goroutines, alloc, failures)
	if err != nil {
		return nil, err
	}
	return t, nil
}

// 各業務HTTPにつきcounterを1増やし、同じ属性で所要秒数をhistogramへ記録する。
func (t *Telemetry) Record(ctx context.Context, method, route string, status int, elapsed time.Duration) {
	attrs := metric.WithAttributes(attribute.String("method", method), attribute.String("route", route), attribute.String("status_class", fmt.Sprintf("%dxx", status/100)))
	t.requests.Add(ctx, 1, attrs)
	t.duration.Record(ctx, elapsed.Seconds(), attrs)
}

// DBはSQL・引数・driverエラー文字列をspanへ記録しない。
func (t *Telemetry) DB(ctx context.Context, operation string) (context.Context, func(error)) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx, func(error) {}
	}
	// 有効なHTTP contextがある場合だけDB client spanを子として開始。SQL文・引数・driverエラーは記録しない。
	ctx, span := t.Tracer.Start(ctx, "db "+operation, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attribute.String("db.system.name", "postgresql"), attribute.String("db.operation.name", operation)))
	return ctx, func(err error) {
		if err != nil {
			span.SetStatus(codes.Error, "database operation failed")
		}
		span.End()
	}
}

// Shutdownはmetric/traceの両providerを、合計で共通の5秒予算内に終了させる。
func (t *Telemetry) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return errors.Join(t.mp.Shutdown(ctx), t.tp.Shutdown(ctx))
}
