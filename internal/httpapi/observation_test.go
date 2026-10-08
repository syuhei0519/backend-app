// メモリ内SDK reader/exporterとhttptestでHTTP計測を厳密確認する。
// 400/404/500/panic・probe除外・親trace継承・HTTP→DB span・ログ/ラベルの情報制限を検証する。
package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"gitlab.com/platform-engineering-lab/backend-app/internal/account"
	"gitlab.com/platform-engineering-lab/backend-app/internal/telemetry"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type observedService struct {
	t               *telemetry.Telemetry
	fail, panicNext bool
	delay           time.Duration
}

func (s *observedService) List(ctx context.Context) ([]account.Account, error) {
	ctx, end := s.t.DB(ctx, "SELECT")
	defer end(nil)
	if s.panicNext {
		panic("private@example.test SQL password")
	}
	if s.fail {
		return nil, errors.New("private@example.test SQL password")
	}
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []account.Account{}, nil
}
func (*observedService) Get(context.Context, int64) (account.Account, error) {
	return account.Account{}, account.ErrNotFound
}
func (*observedService) Create(context.Context, account.Input) (account.Account, error) {
	return account.Account{}, account.ErrInvalid
}
func (*observedService) Update(context.Context, int64, account.Input) (account.Account, error) {
	return account.Account{}, account.ErrInvalid
}
func (*observedService) Delete(context.Context, int64) error { return account.ErrNotFound }
func (*observedService) Ready(context.Context) error         { return nil }

// 業務6要求だけがcounter/histogramのcountへ入り、probe2件を除外することを確認する。
// 固定traceparentの親IDとHTTP/DB親子IDを調べ、二重計数やcontext切断を防ぐ。
func TestExactRequestsHistogramPrivacyAndTraceParent(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Name: "pe_http_server_request_duration"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: telemetry.DurationBounds}})))
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer mp.Shutdown(context.Background())
	defer tp.Shutdown(context.Background())
	obs, err := telemetry.New(mp, tp, resource.Empty(), "test-instance", "test-version")
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	svc := &observedService{t: obs, delay: 30 * time.Millisecond}
	srv := NewWithTelemetry(svc, svc, slog.New(slog.NewJSONHandler(&logs, nil)), obs)
	tests := []struct {
		path            string
		want            int
		fail, panicNext bool
	}{
		{"/api/accounts?email=private@example.test", 200, false, false},
		{"/api/accounts/invalid-private@example.test", 400, false, false},
		{"/private@example.test/not-a-route", 404, false, false},
		{"/api/accounts", 500, true, false},
		{"/api/accounts", 500, false, true},
		{"/api/accounts/12", 404, false, false},
		{"/livez", 200, false, false},
		{"/readyz", 200, false, false},
	}
	for _, tc := range tests {
		svc.fail, svc.panicNext = tc.fail, tc.panicNext
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", "Bearer private-secret")
		r.Header.Set("Cookie", "private-secret")
		r.Header.Set("X-Request-ID", "private@example.test")
		r.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: status %d", tc.path, w.Code)
		}
		if w.Header().Get("X-Request-ID") == "private@example.test" {
			t.Fatal("unsafe request ID echoed")
		}
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	var total, histogramCount uint64
	var durationSum float64
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				if m.Name != "pe_http_server_requests" || !d.IsMonotonic || d.Temporality != metricdata.CumulativeTemporality {
					t.Fatal("counter contract")
				}
				for _, point := range d.DataPoints {
					total += uint64(point.Value)
					for _, a := range point.Attributes.ToSlice() {
						if strings.Contains(a.Value.AsString(), "private") {
							t.Fatal("PII metric")
						}
					}
				}
			case metricdata.Histogram[float64]:
				if m.Name != "pe_http_server_request_duration" || m.Unit != "s" || d.Temporality != metricdata.CumulativeTemporality {
					t.Fatal("duration contract")
				}
				for _, point := range d.DataPoints {
					histogramCount += point.Count
					if !reflect.DeepEqual(point.Bounds, telemetry.DurationBounds) {
						t.Fatal("bounds")
					}
					durationSum += point.Sum
				}
			}
		}
	}
	if total != 6 || histogramCount != 6 {
		t.Fatalf("N business requests=N count: %d/%d", total, histogramCount)
	}
	if durationSum < 0.03 {
		t.Fatal("known 30ms delay missing from seconds histogram")
	}
	spans := exporter.GetSpans()
	servers, children := 0, 0
	for _, span := range spans {
		if span.SpanKind.String() == "server" {
			servers++
			if span.Parent.SpanID().String() != "2222222222222222" {
				t.Fatal("W3C parent lost")
			}
		}
		if strings.HasPrefix(span.Name, "db ") {
			children++
			if !span.Parent.IsValid() || span.Parent.SpanID().String() == "2222222222222222" {
				t.Fatal("DB not child of HTTP")
			}
		}
		if span.SpanContext.TraceID().String() != "11111111111111111111111111111111" {
			t.Fatal("trace ID lost")
		}
		for _, a := range span.Attributes {
			if strings.Contains(a.Value.AsString(), "private") {
				t.Fatal("PII trace")
			}
		}
	}
	if servers != 6 || children != 3 {
		t.Fatalf("HTTP/DB spans %d/%d", servers, children)
	}
	for _, forbidden := range []string{"private@example.test", "private-secret", "SQL", "password"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("PII log: %s", forbidden)
		}
	}
	if !strings.Contains(logs.String(), `"trace_id":"11111111111111111111111111111111"`) || !strings.Contains(logs.String(), `"service.instance.id":"test-instance"`) {
		t.Fatal("log correlation missing")
	}
	t.Logf("6 business requests / 2 health: counter=%d histogram=%d; server=%d DB=%d; explicit seconds bounds=%v; privacy and parent continuity passed", total, histogramCount, servers, children, telemetry.DurationBounds)
}
