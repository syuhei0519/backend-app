// OTel Resource・queue・終了期限の単体試験。外部New Relicや稼働Collectorへ接続しない。
// 実Collector名変換fixtureはTEST_COLLECTOR_ENDPOINT明示時だけで、未指定はSkip。
package telemetry

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

const fixtureVersion = "5a21bf5ebf4ea1ee0f54c53cce1bdd87814adbfd"

func clearOTLPExporterEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OTEL_EXPORTER_OTLP_") {
			t.Setenv(key, "")
		}
	}
}

// 起動ごとのinstance UUIDとsource version、許可外endpoint拒否を確認する。
func TestResourceInstanceAndRestrictedDestination(t *testing.T) {
	clearOTLPExporterEnvironment(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	a, err := Init(context.Background(), fixtureVersion, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Shutdown()
	b, err := Init(context.Background(), fixtureVersion, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Shutdown()
	if a.Instance == b.Instance || len(a.Instance) != 36 {
		t.Fatal("process instance must be UUID, never Pod UID")
	}
	if a.Version != fixtureVersion {
		t.Fatal("build-time version changed")
	}
	t.Setenv("POD_NAMESPACE", "account")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "local")
	for _, endpoint := range []string{"https://otlp.nr-data.net", "http://attacker.test:4318", "http://user:pass@localhost:4318", "http://localhost:4318/?key=value"} {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
		if _, err := Init(context.Background(), fixtureVersion, logger); err == nil {
			t.Fatalf("destination accepted: %s", endpoint)
		}
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	if _, err := Init(context.Background(), "unknown", logger); err == nil {
		t.Fatal("enabled telemetry needs source SHA")
	}
}

// ローカル503 receiverに対して有限queueへ計測を入れ、要求経路非blockingと5秒終了を確認する。
// データ無損失を期待する試験ではない。receiver.Closeとprovider.Shutdownで一時資源を回収する。
func TestQueueDoesNotBlockRequestsAndShutdownIsBounded(t *testing.T) {
	clearOTLPExporterEnvironment(t)
	// 意図的に利用不能なreceiverを使う。再試行なし、exportは3秒以内、両providerの終了は合計5秒期限。
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer receiver.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.URL)
	t.Setenv("POD_NAMESPACE", "account")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "local")
	obs, err := Init(context.Background(), fixtureVersion, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for i := 0; i < 10000; i++ {
		_, span := obs.Tracer.Start(context.Background(), "bounded-fixture")
		span.End()
		obs.Record(context.Background(), "GET", "/api/accounts", 200, time.Millisecond)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("bounded queue blocked request path")
	}
	started = time.Now()
	_ = obs.Shutdown()
	if time.Since(started) > 5500*time.Millisecond {
		t.Fatal("shutdown exceeded shared 5s budget")
	}
	if obs.exportErrors.Load() == 0 {
		t.Fatal("export failure not observed")
	}
	t.Log("10000 spans+measurements completed without blocking; unavailable export failure observed; shutdown <=5s")
}

func TestSDKCollectorMetricNames(t *testing.T) {
	clearOTLPExporterEnvironment(t)
	endpoint := strings.TrimSpace(os.Getenv("TEST_COLLECTOR_ENDPOINT"))
	if endpoint == "" {
		t.Skip("explicit disposable Collector fixture required")
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	t.Setenv("POD_NAMESPACE", "account")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "local")
	obs, err := Init(context.Background(), fixtureVersion, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		obs.Record(context.Background(), "GET", "/sdk-fixture", 200, 30*time.Millisecond)
	}
	if err := obs.Shutdown(); err != nil {
		t.Fatal(err)
	}
	t.Logf("SDK actual OTLP cumulative7, seconds histogram7, instance=%s version=%s; scrape and names checked by acceptance script", obs.Instance, obs.Version)
}

// 各caseをSDKのglobal logger/error handlerが未変更のfresh processで実行する。
func TestInitEnvironmentSubprocess(t *testing.T) {
	const marker = "PRIVATE_MARKER"
	if os.Getenv("ACCOUNT_TELEMETRY_INIT_HELPER") == "1" {
		received := make(chan string, 8)
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil || len(body) == 0 || bytes.Contains(body, []byte(marker)) || strings.Contains(fmt.Sprint(r.Header), marker) {
				t.Error("unexpected telemetry export payload")
			}
			received <- r.URL.Path
			w.WriteHeader(http.StatusOK)
		}))
		defer receiver.Close()
		if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
			if err := os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", receiver.URL); err != nil {
				t.Fatal("set fixture endpoint")
			}
		}
		obs, err := Init(context.Background(), fixtureVersion, slog.New(slog.NewTextHandler(os.Stderr, nil)))
		if os.Getenv("ACCOUNT_TELEMETRY_EXPECT_ERROR") == "1" {
			if err == nil {
				_ = obs.Shutdown()
				t.Fatal("unsupported environment accepted")
			}
			fmt.Fprintln(os.Stderr, err)
			return
		}
		if err != nil {
			t.Fatal("normal initialization rejected")
		}
		defer obs.Shutdown()
		_, span := obs.Tracer.Start(context.Background(), "synthetic")
		span.End()
		obs.Record(context.Background(), "GET", "/synthetic", http.StatusOK, time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if obs.tp.ForceFlush(ctx) != nil {
			t.Fatal("normal trace export failed")
		}
		if obs.mp.ForceFlush(ctx) != nil {
			t.Fatal("normal metric export failed")
		}
		seen := map[string]bool{}
		for len(seen) < 2 {
			select {
			case path := <-received:
				seen[path] = true
			case <-ctx.Done():
				t.Fatal("normal trace and metric did not reach receiver")
			}
		}
		if !seen["/v1/traces"] || !seen["/v1/metrics"] {
			t.Fatal("unexpected export paths")
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal("test executable")
	}
	for _, c := range []struct {
		name, input, expectedStderr string
	}{
		{"global_headers", "OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer%" + marker, "unsupported Account telemetry environment\n"},
		{"trace_headers", "OTEL_EXPORTER_OTLP_TRACES_HEADERS=Authorization=Bearer%" + marker, "unsupported Account telemetry environment\n"},
		{"trace_endpoint_userinfo", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://user:" + marker + "@collector.invalid:4318", "unsupported Account telemetry environment\n"},
		{"metric_headers", "OTEL_EXPORTER_OTLP_METRICS_HEADERS=Authorization=Bearer%" + marker, "unsupported Account telemetry environment\n"},
		{"metric_endpoint_userinfo", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://user:" + marker + "@collector.invalid:4318", "unsupported Account telemetry environment\n"},
		{"global_endpoint_userinfo", "OTEL_EXPORTER_OTLP_ENDPOINT=http://user:" + marker + "@127.0.0.1:4318", "invalid Collector endpoint\n"},
		{"normal", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestInitEnvironmentSubprocess$")
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "ACCOUNT_TELEMETRY_INIT_HELPER=1", "POD_NAMESPACE=account", "DEPLOYMENT_ENVIRONMENT=local"}
			if c.input != "" {
				cmd.Env = append(cmd.Env, c.input, "ACCOUNT_TELEMETRY_EXPECT_ERROR=1")
			}
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if cmd.Run() != nil {
				t.Fatal("fresh subprocess failed (output withheld)")
			}
			output := stdout.String() + stderr.String()
			if strings.Contains(output, marker) || strings.Contains(output, "collector.invalid") {
				t.Fatal("private value leaked (output withheld)")
			}
			if stderr.String() != c.expectedStderr {
				t.Fatal("unexpected initialization stderr (output withheld)")
			}
		})
	}
}

// Destination/namespace pairing is checked without opening an exporter or socket.
func TestCollectorNamespaceIdentityPairs(t *testing.T) {
	shared := "http://otel-collector.observability.svc.cluster.local:4318"
	dedicated := "http://account-release-otel-collector.observability.svc.cluster.local:4318"
	for _, tc := range []struct {
		name, endpoint, namespace, environment, version string
		allowed                                         bool
	}{
		{"legacy-account-transition", shared, "account", "local", fixtureVersion, true},
		{"current-existing-kind-account", shared, "learning-services-20261018", "local", fixtureVersion, true},
		{"fresh-learning-account-release", dedicated, "learning-account-release", "local", fixtureVersion, true},
		{"current-crossed-dedicated", dedicated, "learning-services-20261018", "local", fixtureVersion, false},
		{"fresh-crossed-shared", shared, "learning-account-release", "local", fixtureVersion, false},
		{"legacy-crossed-dedicated", dedicated, "account", "local", fixtureVersion, false},
		{"other-namespace", dedicated, "sonarqube", "local", fixtureVersion, false},
		{"other-collector", "http://unknown.observability.svc.cluster.local:4318", "learning-account-release", "local", fixtureVersion, false},
		{"wrong-environment", dedicated, "learning-account-release", "production", fixtureVersion, false},
		{"untrusted-source", dedicated, "learning-account-release", "local", "unknown", false},
		{"credential-url", "http://user:pass@account-release-otel-collector.observability.svc.cluster.local:4318", "learning-account-release", "local", fixtureVersion, false},
		{"query-url", dedicated + "?key=value", "learning-account-release", "local", fixtureVersion, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDestination(tc.endpoint, tc.namespace, tc.environment, tc.version)
			if (err == nil) != tc.allowed {
				t.Fatalf("destination/identity decision mismatch: %v", err)
			}
		})
	}
}
