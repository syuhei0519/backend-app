package ordertrace

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDestinationAndOrderIdentity(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "order")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "local")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, endpoint := range []string{"https://otlp.nr-data.net", "http://example.invalid:4318", "http://user:pass@localhost:4318", "http://localhost:4318?private=x"} {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
		if _, e := Init(context.Background(), "0a55a61e55badebc2e1fb688270c7a21b315a815", logger); e == nil {
			t.Fatal("invalid destination accepted")
		}
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	for _, identity := range []struct{ version, namespace string }{{"unknown", "order"}, {"0a55a61e55badebc2e1fb688270c7a21b315a815", "account"}} {
		t.Setenv("POD_NAMESPACE", identity.namespace)
		if _, e := Init(context.Background(), identity.version, logger); e == nil {
			t.Fatal("invalid Order identity accepted")
		}
	}
}

func TestFullQueueDoesNotBlockAndShutdownBudget(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	}))
	defer receiver.Close()
	defer close(release)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.URL)
	t.Setenv("POD_NAMESPACE", "order")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "local")
	provider, e := Init(context.Background(), "0a55a61e55badebc2e1fb688270c7a21b315a815", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if e != nil {
		t.Fatal(e)
	}
	tracer := provider.Tracer("order-backend")
	for i := 0; i < 64; i++ {
		_, s := tracer.Start(context.Background(), "synthetic")
		s.End()
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("export did not start")
	}
	start := time.Now()
	for i := 0; i < 1024; i++ {
		_, s := tracer.Start(context.Background(), "synthetic")
		s.End()
	}
	if time.Since(start) > time.Second {
		t.Fatal("full queue blocked request path")
	}
	start = time.Now()
	Shutdown(provider)
	if time.Since(start) > 6*time.Second {
		t.Fatal("shutdown exceeded budget")
	}
}
