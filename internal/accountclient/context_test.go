package accountclient

import (
	"context"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel/propagation"
)

func TestUnsafeContextIsReplacedOrDropped(t *testing.T) {
	for _, value := range []string{"", "private-user@example.invalid", "not-a-request-id"} {
		h := http.Header{"X-Request-Id": {value}, "Traceparent": {"invalid"}, "Tracestate": {"private=value"}}
		ctx := Extract(context.Background(), h)
		id := RequestID(ctx)
		if !safeRequestID.MatchString(id) || id == value {
			t.Fatal("unsafe request ID retained")
		}
		out := http.Header{}
		(propagation.TraceContext{}).Inject(ctx, propagation.HeaderCarrier(out))
		if out.Get("Traceparent") != "" || out.Get("Tracestate") != "" {
			t.Fatal("invalid trace context retained")
		}
	}
}
