// Order の HTTP 処理と Account 呼び出しが同じトレースで追えることを確認します。
package orderhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/syuhei0519/backend-app/internal/accountclient"
	"github.com/syuhei0519/backend-app/internal/order"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// Runs in ordinary CI; network boundary, exact parenting, failure and privacy.
func TestOrderAccountSpans(t *testing.T) {
	for _, scenario := range []string{"success", "error", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			defer tp.Shutdown(context.Background())
			tracer := tp.Tracer("test")
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := (propagation.TraceContext{}).Extract(r.Context(), propagation.HeaderCarrier(r.Header))
				_, s := tracer.Start(ctx, "account", trace.WithSpanKind(trace.SpanKindServer))
				defer s.End()
				if r.Header.Get("Authorization") != "" || r.Header.Get("Baggage") != "" {
					t.Error("private header forwarded")
				}
				switch scenario {
				case "timeout":
					<-r.Context().Done()
					return
				case "error":
					w.WriteHeader(503)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"id":1,"email":"private@example.invalid"}`))
			}))
			defer downstream.Close()
			checker, e := accountclient.NewWithTracer(downstream.URL, tracer)
			if e != nil {
				t.Fatal(e)
			}
			defer checker.Close()
			f := &fakeStore{}
			handler := New(order.New(f, checker), f)
			handler.SetTracer(tracer)
			req := httptest.NewRequest("POST", "/api/orders?private=hidden", strings.NewReader(valid))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "private")
			req.Header.Set("Baggage", "private=value")
			req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-1111111111111111-01")
			if scenario == "timeout" {
				ctx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
				defer cancel()
				req = req.WithContext(ctx)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			want := 201
			if scenario == "error" {
				want = 503
			}
			if scenario == "timeout" {
				want = 504
			}
			if response.Code != want {
				t.Fatalf("status %d", response.Code)
			}
			// Wait only for cancelled downstream handler to finish its span.
			for i := 0; i < 100 && len(recorder.Ended()) < 3; i++ {
				time.Sleep(time.Millisecond)
			}
			spans := recorder.Ended()
			if len(spans) != 3 {
				t.Fatalf("spans %d", len(spans))
			}
			var server, client, account sdktrace.ReadOnlySpan
			for _, s := range spans {
				if s.Name() == "POST /api/orders" {
					server = s
				} else if s.Name() == "GET /api/accounts/{id}" {
					client = s
				} else {
					account = s
				}
			}
			if server.Parent().SpanID().String() != "1111111111111111" || client.Parent().SpanID() != server.SpanContext().SpanID() || account.Parent().SpanID() != client.SpanContext().SpanID() {
				t.Fatal("parent chain")
			}
			if client.SpanContext().TraceID() != server.SpanContext().TraceID() || account.SpanContext().TraceID() != server.SpanContext().TraceID() {
				t.Fatal("trace split")
			}
			for _, s := range spans {
				if !s.EndTime().After(s.StartTime()) {
					t.Fatal("latency missing")
				}
				for _, a := range s.Attributes() {
					switch a.Key {
					case attribute.Key("http.request.method"), attribute.Key("http.route"), attribute.Key("http.response.status_code"), attribute.Key("error.type"):
					default:
						t.Fatalf("unexpected attribute %s", a.Key)
					}
				}
				if len(s.Events()) > 0 {
					t.Fatal("raw error event")
				}
			}
			if scenario != "success" && (client.Status().Code != codes.Error || server.Status().Code != codes.Error) {
				t.Fatal("error status")
			}
		})
	}
}
