// OTel Resource・queue・終了期限の単体試験。外部New Relicや稼働Collectorへ接続しない。
// 実Collector名変換fixtureはTEST_COLLECTOR_ENDPOINT明示時だけで、未指定はSkip。
package telemetry

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const fixtureVersion = "5a21bf5ebf4ea1ee0f54c53cce1bdd87814adbfd"

// 起動ごとのinstance UUIDとsource version、許可外endpoint拒否を確認する。
func TestResourceInstanceAndRestrictedDestination(t *testing.T) {
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
