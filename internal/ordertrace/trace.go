// Order サーバーでトレースを初期化し、終了時に送信処理を片付けます。
// Package ordertrace owns the bounded, optional Order trace exporter.
package ordertrace

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Init uses an explicit Order resource contract; Account metrics/identity are untouched.
// Environment headers, retry and TLS credentials are never accepted as implicit input.
func Init(ctx context.Context, version string, logger *slog.Logger) (*sdktrace.TracerProvider, error) {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "order-backend"), attribute.String("service.version", version), attribute.String("service.instance.id", uuid.NewString()), attribute.String("deployment.environment.name", os.Getenv("DEPLOYMENT_ENVIRONMENT")), attribute.String("k8s.namespace.name", os.Getenv("POD_NAMESPACE")))),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	}
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "http" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Host != "otel-collector.observability.svc.cluster.local:4318" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(version) || os.Getenv("POD_NAMESPACE") != "order" || os.Getenv("DEPLOYMENT_ENVIRONMENT") != "local" {
			return nil, errors.New("invalid Order telemetry configuration")
		}
		// SDK v1.47 parses environment before applying explicit options and may
		// log malformed values. Reject unsupported trace exporter input before
		// that parser runs; leave other signals and process logging untouched.
		for _, suffix := range []string{"HEADERS", "CERTIFICATE", "CLIENT_CERTIFICATE", "CLIENT_KEY", "INSECURE", "COMPRESSION", "PROTOCOL", "TIMEOUT", "TRACES_ENDPOINT", "TRACES_HEADERS", "TRACES_CERTIFICATE", "TRACES_CLIENT_CERTIFICATE", "TRACES_CLIENT_KEY", "TRACES_INSECURE", "TRACES_COMPRESSION", "TRACES_PROTOCOL", "TRACES_TIMEOUT"} {
			if os.Getenv("OTEL_EXPORTER_OTLP_"+suffix) != "" {
				return nil, errors.New("unsupported Order telemetry environment")
			}
		}
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint), otlptracehttp.WithURLPath("/v1/traces"), otlptracehttp.WithHeaders(map[string]string{}), otlptracehttp.WithTimeout(3*time.Second), otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}))
		if err != nil {
			return nil, errors.New("Order trace exporter initialization failed")
		}
		opts = append(opts, sdktrace.WithBatcher(exporter, sdktrace.WithMaxQueueSize(256), sdktrace.WithMaxExportBatchSize(64), sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(3*time.Second)))
	}
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) { logger.Warn("telemetry export failed", "service", "order-backend") }))
	return sdktrace.NewTracerProvider(opts...), nil
}

// Shutdown flushes on process termination with a separate finite budget.
func Shutdown(provider *sdktrace.TracerProvider) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = provider.Shutdown(ctx)
}
