// 隔離環境で Order から Account へのトレースを採取する、明示的に有効化したときだけ動く試験です。
package distributedtrace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/account"
	"github.com/syuhei0519/backend-app/internal/httpapi"
	"github.com/syuhei0519/backend-app/internal/ordertest"
	"github.com/syuhei0519/backend-app/internal/store/postgres"
	"github.com/syuhei0519/backend-app/internal/telemetry"
)

const collectorImage = "ghcr.io/open-telemetry/opentelemetry-collector-releases/opentelemetry-collector-contrib:0.161.0@sha256:b5cf983651c32c3ca13f936deb51742015a54d121f388cac248923ddeb8cc9fc"

func docker(t *testing.T, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp"}
	if cmd.Run() != nil {
		t.Fatal("owned disposable Collector operation failed")
	}
}
func address(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal("allocate loopback")
	}
	a := l.Addr().String()
	l.Close()
	return a
}

// Opt-in actual HTTP/DB/export bytes. No shared DB, external sink or cluster.
func TestCloudExportedTrace(t *testing.T) {
	ordertest.Enabled(t)
	sourceBytes, sourceErr := exec.Command("git", "rev-parse", "HEAD").Output()
	if sourceErr != nil {
		t.Fatal("source revision")
	}
	sourceSHA := strings.TrimSpace(string(sourceBytes))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dir := t.TempDir()
	evidence := filepath.Join(dir, "evidence")
	if os.Mkdir(evidence, 0777) != nil || os.Chmod(evidence, 0777) != nil {
		t.Fatal("fixture directory")
	}
	config := filepath.Join(dir, "collector.yaml")
	configBytes, err := os.ReadFile("collector.yaml")
	if err != nil || os.WriteFile(config, configBytes, 0644) != nil || os.Chmod(config, 0644) != nil {
		t.Fatal("Collector config fixture")
	}
	name := "trace15-" + uuid.NewString()
	endpoint := "http://" + address(t)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(endpoint, "http://"))
	docker(t, "create", "--pull=never", "--name", name, "--label", "trace15-disposable=true", "--publish", "127.0.0.1:"+port+":4318", "--mount", "type=bind,src="+config+",dst=/config.yaml,readonly", "--mount", "type=bind,src="+evidence+",dst=/evidence", collectorImage, "--config=/config.yaml")
	t.Cleanup(func() { docker(t, "rm", "--force", "--volumes", name) })
	docker(t, "start", name)
	waitCollector := func() {
		t.Helper()
		for i := 0; i < 100; i++ {
			r, e := http.Post(endpoint+"/v1/traces", "application/json", strings.NewReader(`{}`))
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("Collector not ready")
	}
	waitCollector()
	accounts := ordertest.Start(t, "accounts", "account_app")
	orders := ordertest.Start(t, "orders", "order_app")
	store, e := postgres.Open(ctx, accounts.URL())
	if e != nil {
		t.Fatal("Account pool")
	}
	defer store.Close()
	if store.Migrate(ctx) != nil {
		t.Fatal("Account migrate")
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)
	t.Setenv("DEPLOYMENT_ENVIRONMENT", "local")
	t.Setenv("POD_NAMESPACE", "account")
	obs, e := telemetry.Init(ctx, sourceSHA, logger)
	if e != nil {
		t.Fatal("Account telemetry")
	}
	defer obs.Shutdown()
	store.SetTelemetry(obs)
	a, e := store.Create(ctx, account.Input{Name: "Synthetic", Email: "trace15@example.invalid"})
	if e != nil {
		t.Fatal("synthetic Account")
	}
	accountHTTP := httptest.NewServer(httpapi.NewWithTelemetry(account.New(store), store, logger, obs))
	defer accountHTTP.Close()
	binary := filepath.Join(dir, "order-server")
	build := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X main.buildCommit="+sourceSHA, "-o", binary, "../../cmd/order-server")
	if build.Run() != nil {
		t.Fatal("Order build")
	}
	orderAddr := address(t)
	env := []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL=" + orders.URL(), "ORDER_HTTP_ADDRESS=" + orderAddr, "ORDER_ACCOUNT_URL=" + accountHTTP.URL, "LOG_LEVEL=error", "OTEL_EXPORTER_OTLP_ENDPOINT=" + endpoint, "DEPLOYMENT_ENVIRONMENT=local", "POD_NAMESPACE=order"}
	migrate := exec.CommandContext(ctx, binary, "migrate")
	migrate.Env = env
	if migrate.Run() != nil {
		t.Fatal("Order migrate")
	}
	process := exec.CommandContext(ctx, binary)
	process.Env = env
	if process.Start() != nil {
		t.Fatal("Order start")
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	defer func() { _ = process.Process.Kill(); <-done }()
	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + orderAddr
	ready := false
	for i := 0; i < 100; i++ {
		r, e := client.Get(base + "/readyz")
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("Order readiness")
	}
	pool, e := pgxpool.New(ctx, orders.URL())
	if e != nil {
		t.Fatal("Order pool")
	}
	defer pool.Close()
	count := func() int {
		var n int
		if pool.QueryRow(ctx, "SELECT count(*) FROM orders").Scan(&n) != nil {
			t.Fatal("Order count")
		}
		return n
	}
	traces := map[string]string{}
	call := func(label string, id int64, status int) time.Duration {
		t.Helper()
		traceID := strings.ReplaceAll(uuid.NewString(), "-", "")
		traces[label] = traceID
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/api/orders?private=synthetic-query", strings.NewReader(fmt.Sprintf(`{"account_id":%d,"item_code":"SYNTHETIC-PRIVATE-ITEM","quantity":1}`, id)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Traceparent", "00-"+traceID+"-1111111111111111-01")
		req.Header.Set("Authorization", "synthetic-private-auth")
		req.Header.Set("Baggage", "private=synthetic-baggage")
		before := count()
		start := time.Now()
		r, e := client.Do(req)
		elapsed := time.Since(start)
		if e != nil {
			t.Fatal("Order HTTP")
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		if r.StatusCode != status {
			t.Fatalf("%s expected %d got %d", label, status, r.StatusCode)
		}
		delta := 0
		if status == 201 {
			delta = 1
		}
		if count() != before+delta {
			t.Fatal("failure wrote Order")
		}
		t.Logf("%s status=%d latency=%s", label, status, elapsed.Round(time.Millisecond))
		return elapsed
	}
	call("success", a.ID, 201)
	call("missing", 9223372036854775807, 422)
	// Real Account DB error, then recovery. Only the DB created above is stopped.
	accounts.Stop()
	call("error", a.ID, 503)
	accounts.Restart()
	// Hold only the synthetic table in the disposable DB; client cancellation propagates.
	locks, e := pgxpool.New(ctx, accounts.URL())
	if e != nil {
		t.Fatal("lock pool")
	}
	defer locks.Close()
	tx, e := locks.Begin(ctx)
	if e != nil {
		t.Fatal("lock transaction")
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, "LOCK TABLE accounts IN ACCESS EXCLUSIVE MODE"); e != nil {
		t.Fatal("lock synthetic Account table")
	}
	elapsed := call("timeout", a.ID, 504)
	if elapsed < 1900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatal("timeout budget")
	}
	if tx.Rollback(ctx) != nil {
		t.Fatal("unlock")
	}
	// Let completed spans reach the file before stopping the owned Collector.
	time.Sleep(3 * time.Second)
	docker(t, "stop", "--time", "5", name)
	if call("collector_down", a.ID, 201) > time.Second {
		t.Fatal("Collector outage blocked API")
	}
	time.Sleep(4 * time.Second) // exceed SDK export/batch window; failed batch may be lost
	docker(t, "start", name)
	waitCollector()
	call("recovered", a.ID, 201)
	time.Sleep(3 * time.Second)
	docker(t, "stop", "--time", "5", name)
	raw, e := os.ReadFile(filepath.Join(evidence, "traces.jsonl"))
	if e != nil || len(raw) == 0 {
		t.Fatal("no exported bytes")
	}
	for _, forbidden := range []string{"trace15@example.invalid", "Synthetic", "SYNTHETIC-PRIVATE-ITEM", "synthetic-query", "synthetic-private-auth", "synthetic-baggage", "SELECT id", "DATABASE_URL"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatal("sensitive trace output")
		}
	}
	validate(t, raw, traces)
	if dest := os.Getenv("TRACE_EVIDENCE_DIR"); dest != "" {
		if os.MkdirAll(dest, 0700) != nil || os.WriteFile(filepath.Join(dest, "traces.jsonl"), raw, 0600) != nil {
			t.Fatal("save sanitized evidence")
		}
		b, _ := json.MarshalIndent(traces, "", "  ")
		if os.WriteFile(filepath.Join(dest, "cases.json"), b, 0600) != nil {
			t.Fatal("save case map")
		}
	}
	t.Logf("exported_bytes=%d; parent/status/duration/attribute validation passed", len(raw))
}
