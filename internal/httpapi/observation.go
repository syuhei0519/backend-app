// HTTPの観測middleware。server.goが呼び、応答status・時間・trace識別子をログとtelemetryへ記録する。
// DB子spanはinternal/store/postgres/store.go→internal/telemetry/telemetry.go。検証はobservation_test.go。
package httpapi

import (
	"gitlab.com/platform-engineering-lab/backend-app/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// 生URLではなくmuxのルート雛形を記録し、IDやquery由来の高いラベル種類数と情報露出を避ける。
func observe(logger *slog.Logger, mux *http.ServeMux, observation *telemetry.Telemetry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		_, pattern := mux.Handler(r)
		route := pattern
		if _, path, ok := strings.Cut(pattern, " "); ok {
			route = path
		}
		if route == "" {
			route = "unmatched"
		}
		method := r.Method
		switch method {
		case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		default:
			method = "OTHER"
		}
		// probeは業務counter/histogram/spanから除外するが、この関数末尾の完了ログは出す。
		health := r.URL.Path == "/livez" || r.URL.Path == "/readyz"
		var span trace.Span
		if observation != nil && !health {
			// traceparent/tracestateをcontextへ取り込みHTTP server spanを開始。spanは処理区間、traceは区間の連なり。
			// r.WithContextで子の業務・DB処理にも親spanを渡す。frontend Nginxは通常の要求ヘッダーを転送する。
			ctx := observation.Propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span = observation.Tracer.Start(ctx, method+" "+route, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String("http.request.method", method), attribute.String("http.route", route)))
			r = r.WithContext(ctx)
		}
		out := &recorder{ResponseWriter: w, status: 200}
		// 成功・エラー・panicで共通の計測/ログ/Span.Endを実行する。
		// panic前に応答済みならstatusを書き換えられないため、実statusとspanのError判定は別になり得る。
		defer func() {
			panicked := recover() != nil
			if panicked && !out.written {
				write(out, 500, map[string]string{"error": "internal_error"})
			}
			elapsed := time.Since(started)
			if span != nil {
				span.SetAttributes(attribute.Int("http.response.status_code", out.status))
				if out.status >= 500 || panicked {
					span.SetStatus(codes.Error, "request failed")
				}
				span.End()
			}
			if observation != nil && !health {
				observation.Record(r.Context(), method, route, out.status, elapsed)
			}
			fields := []any{"service", "account-backend", "request_id", r.Context().Value(requestKey), "method", method, "route", route, "status", out.status, "duration_ms", elapsed.Milliseconds()}
			if observation != nil {
				fields = append(fields, "service.version", observation.Version, "service.instance.id", observation.Instance)
			}
			// ログのspan_idはHTTP span自身。DB spanのparent IDと対応し、trace_idで同一要求の処理をまとめて探せる。
			if sc := trace.SpanContextFromContext(r.Context()); sc.IsValid() {
				fields = append(fields, "trace_id", sc.TraceID().String(), "span_id", sc.SpanID().String())
			}
			logger.InfoContext(r.Context(), "request completed", fields...)
		}()
		limit(mux).ServeHTTP(out, r)
	})
}
