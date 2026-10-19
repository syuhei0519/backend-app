package ordertrace

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
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
	// This fixture owns its exporter. Ignore inherited OTLP settings only for
	// this test; t.Setenv restores each value without changing production Init.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OTEL_EXPORTER_OTLP_") {
			t.Setenv(key, "")
		}
	}
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

// Every case starts before any SDK global logger/error-handler changes.
func TestInitEnvironmentSubprocess(t *testing.T) {
	const marker = "PRIVATE_MARKER"
	if os.Getenv("ORDER_TRACE_INIT_HELPER") == "1" {
		exported := make(chan struct{}, 1)
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil || len(body) == 0 || bytes.Contains(body, []byte(marker)) || strings.Contains(fmt.Sprint(r.Header), marker) {
				t.Error("unexpected trace export payload")
			}
			exported <- struct{}{}
			w.WriteHeader(http.StatusOK)
		}))
		defer receiver.Close()
		os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.URL)
		provider, err := Init(context.Background(), "0a55a61e55badebc2e1fb688270c7a21b315a815", slog.New(slog.NewTextHandler(os.Stderr, nil)))
		if os.Getenv("ORDER_TRACE_EXPECT_REJECT") == "1" {
			if err == nil {
				Shutdown(provider)
				t.Fatal("unsupported environment accepted")
			}
			fmt.Fprintln(os.Stderr, err)
			return
		}
		if err != nil {
			t.Fatal("normal initialization rejected")
		}
		defer Shutdown(provider)
		_, s := provider.Tracer("order-backend").Start(context.Background(), "synthetic")
		s.End()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if provider.ForceFlush(ctx) != nil {
			t.Fatal("normal export failed")
		}
		select {
		case <-exported:
		default:
			t.Fatal("normal trace did not reach receiver")
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("test executable")
	}
	for _, c := range []struct{ name, input string }{
		{"malformed_headers", "OTEL_EXPORTER_OTLP_TRACES_HEADERS=Authorization=Bearer%" + marker},
		{"malformed_endpoint", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://collector.invalid/%" + marker},
		{"normal", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInitEnvironmentSubprocess$")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "ORDER_TRACE_INIT_HELPER=1", "POD_NAMESPACE=order", "DEPLOYMENT_ENVIRONMENT=local"}
			expectedStderr := ""
			if c.input != "" {
				cmd.Env = append(cmd.Env, c.input, "ORDER_TRACE_EXPECT_REJECT=1")
				expectedStderr = "unsupported Order telemetry environment\n"
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if cmd.Run() != nil {
				t.Fatal("fresh subprocess failed (output withheld)")
			}
			if strings.Contains(stdout.String()+stderr.String(), marker) || strings.Contains(stdout.String()+stderr.String(), "collector.invalid") {
				t.Fatal("private value leaked (output withheld)")
			}
			if stderr.String() != expectedStderr {
				t.Fatal("unexpected initialization stderr (output withheld)")
			}
		})
	}
}

// Exercise the queue fixture under conflicting inherited exporter settings and
// verify that its cleanup restores the caller's environment.
func TestQueueFixtureWithInheritedExporterEnvironment(t *testing.T) {
	inherited := map[string]string{
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://127.0.0.1:4318",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS":  "Authorization=Bearer%PRIVATE_MARKER",
		"OTEL_EXPORTER_OTLP_TIMEOUT":         "PRIVATE_MARKER",
	}
	for key, value := range inherited {
		t.Setenv(key, value)
	}
	t.Run("queue", TestFullQueueDoesNotBlockAndShutdownBudget)
	for key, value := range inherited {
		if os.Getenv(key) != value {
			t.Fatal("queue fixture did not restore inherited environment")
		}
	}
}
