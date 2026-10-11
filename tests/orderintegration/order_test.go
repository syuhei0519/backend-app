// Real HTTP, independent OS processes and fresh PostgreSQL containers only.
package orderintegration

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
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gitlab.com/platform-engineering-lab/backend-app/internal/account"
	"gitlab.com/platform-engineering-lab/backend-app/internal/httpapi"
	"gitlab.com/platform-engineering-lab/backend-app/internal/order"
	"gitlab.com/platform-engineering-lab/backend-app/internal/ordertest"
	"gitlab.com/platform-engineering-lab/backend-app/internal/store/postgres"
)

func TestDisposableHTTPPersistenceAndAccount(t *testing.T) {
	ordertest.Enabled(t)
	// Separate engines and fresh anonymous volumes: no shared/live DB access.
	accounts := ordertest.Start(t, "accounts", "account_app")
	orders := ordertest.Start(t, "orders", "order_app")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	acct, err := postgres.Open(ctx, accounts.URL())
	if err != nil {
		t.Fatal("Account open")
	}
	defer acct.Close()
	if acct.Migrate(ctx) != nil || acct.Ready(ctx) != nil {
		t.Fatal("Account migration/ready")
	}
	accountHTTP := httptest.NewServer(httpapi.New(account.New(acct), acct, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer accountHTTP.Close()
	accountPool, err := pgxpool.New(ctx, accounts.URL())
	if err != nil {
		t.Fatal("fixture pool")
	}
	defer accountPool.Close()
	orderPool, err := pgxpool.New(ctx, orders.URL())
	if err != nil {
		t.Fatal("Order pool")
	}
	defer orderPool.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	call := func(method, url, body, media string, status int) ([]byte, http.Header) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
		if err != nil {
			t.Fatal("request")
		}
		if media != "" {
			req.Header.Set("Content-Type", media)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("HTTP transport failed")
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal("HTTP read failed")
		}
		if res.StatusCode != status {
			t.Fatalf("%s expected %d got %d", method, status, res.StatusCode)
		}
		return raw, res.Header
	}
	createAccount := func(name, email string) account.Account {
		raw, _ := call("POST", accountHTTP.URL+"/api/accounts", fmt.Sprintf(`{"name":%q,"email":%q}`, name, email), "application/json", 201)
		var a account.Account
		if json.Unmarshal(raw, &a) != nil || a.ID < 1 {
			t.Fatal("Account response")
		}
		return a
	}
	sentinel := createAccount("Preserve", "preserve@example.invalid")
	snapshot := func() string {
		var out string
		if accountPool.QueryRow(ctx, `SELECT json_build_object('sentinel',(SELECT row_to_json(a) FROM accounts a WHERE id=$1),'history',(SELECT json_agg(h ORDER BY version) FROM schema_migrations h),'max',(SELECT max(version) FROM schema_migrations))::text`, sentinel.ID).Scan(&out) != nil {
			t.Fatal("Account snapshot")
		}
		return out
	}
	before := snapshot()
	// CRUD fixture is distinct from the preserved sentinel.
	a := createAccount("Before", "before@example.invalid")
	call("GET", accountHTTP.URL+"/api/accounts", "", "", 200)
	accountPath := accountHTTP.URL + fmt.Sprintf("/api/accounts/%d", a.ID)
	call("GET", accountPath, "", "", 200)
	call("PUT", accountPath, `{"name":"After","email":"after@example.invalid"}`, "application/json", 200)
	call("POST", accountHTTP.URL+"/api/accounts", `{"name":"Duplicate","email":"after@example.invalid"}`, "application/json", 409)
	call("POST", accountHTTP.URL+"/api/accounts", `{"name":"","email":"invalid"}`, "application/json", 400)
	call("GET", accountHTTP.URL+"/api/accounts/9223372036854775807", "", "", 404)
	binary := filepath.Join(t.TempDir(), "order-server")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/order-server")
	if build.Run() != nil {
		t.Fatal("build Order process failed")
	}
	processEnv := func(address string) []string {
		return []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL=" + orders.URL(), "ORDER_HTTP_ADDRESS=" + address, "LOG_LEVEL=error"}
	}
	migrate := func() {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, "migrate")
		cmd.Env = processEnv("127.0.0.1:0")
		if cmd.Run() != nil {
			t.Fatal("Order migration process failed")
		}
	}
	migrate()
	migrate()
	var history string
	if orderPool.QueryRow(ctx, `SELECT json_agg(h ORDER BY version)::text FROM order_schema_migrations h`).Scan(&history) != nil {
		t.Fatal("Order history")
	}
	type process struct {
		cmd     *exec.Cmd
		done    chan error
		base    string
		stopped bool
	}
	start := func() *process {
		t.Helper()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal("allocate HTTP port")
		}
		address := listener.Addr().String()
		listener.Close()
		p := &process{cmd: exec.CommandContext(ctx, binary), done: make(chan error, 1), base: "http://" + address}
		p.cmd.Env = processEnv(address)
		if p.cmd.Start() != nil {
			t.Fatal("start Order process")
		}
		go func() { p.done <- p.cmd.Wait() }()
		t.Cleanup(func() {
			if !p.stopped {
				_ = p.cmd.Process.Kill()
				<-p.done
			}
		})
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			res, err := client.Get(p.base + "/readyz")
			if err == nil {
				res.Body.Close()
				if res.StatusCode == 200 {
					return p
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("Order process did not become ready")
		return nil
	}
	stop := func(p *process) {
		t.Helper()
		if p.cmd.Process.Signal(syscall.SIGTERM) != nil {
			t.Fatal("signal Order process")
		}
		deadline := time.Now().Add(2 * time.Second)
		draining := false
		for time.Now().Before(deadline) {
			res, err := client.Get(p.base + "/readyz")
			if err == nil {
				res.Body.Close()
				if res.StatusCode == 503 {
					draining = true
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !draining {
			t.Error("shutdown readiness was not 503")
		}
		if err := <-p.done; err != nil {
			t.Error("Order shutdown failed")
		}
		p.stopped = true
	}
	p := start()
	initialPID := p.cmd.Process.Pid
	payload := fmt.Sprintf(`{"account_id":%d,"item_code":"SKU-001","quantity":2}`, a.ID)
	raw, headers := call("POST", p.base+"/api/orders", payload, "application/json; charset=utf-8", 201)
	var created order.Order
	if json.Unmarshal(raw, &created) != nil || created.ID < 1 || created.AccountID != a.ID || created.Quantity != 2 || created.ItemCode != "SKU-001" || created.CreatedAt.IsZero() {
		t.Fatal("Order representation")
	}
	path := fmt.Sprintf("/api/orders/%d", created.ID)
	if headers.Get("Location") != path {
		t.Fatal("Order Location")
	}
	same := func(base string) {
		t.Helper()
		got, _ := call("GET", base+path, "", "", 200)
		if !bytes.Equal(got, raw) {
			t.Fatal("persisted Order changed")
		}
	}
	same(p.base)
	call("DELETE", accountPath, "", "", 204)
	call("GET", accountPath, "", "", 404)
	same(p.base)
	call("POST", p.base+"/api/orders", `{"account_id":9223372036854775807,"item_code":"A","quantity":999}`, "application/json", 201)
	call("GET", p.base+"/api/orders/9223372036854775807", "", "", 404)
	boundary := fmt.Sprintf(`{"account_id":1,"item_code":%q,"quantity":1}`, strings.Repeat("a", 64))
	call("POST", p.base+"/api/orders", boundary+strings.Repeat(" ", 16384-len(boundary)), "application/json", 201)
	count := func() int {
		var n int
		if orderPool.QueryRow(ctx, `SELECT count(*) FROM orders`).Scan(&n) != nil {
			t.Fatal("Order row count")
		}
		return n
	}
	n := count()
	invalid := []string{"", "null", "[]", "true", "1", `"s"`, `{}`, payload + ` {}`, payload + ` x`, payload[:len(payload)-1], `{"account_id":1,"account_id":2,"item_code":"A","quantity":1}`, `{"account_id":1,"\u0061ccount_id":2,"item_code":"A","quantity":1}`}
	for _, key := range []string{"account_id", "item_code", "quantity"} {
		values := []string{"null", "{}", "[]", "true"}
		if key != "item_code" {
			values = append(values, `"1"`, "1.0", "1e0", "0", "-1", "9223372036854775808")
		} else {
			values = append(values, `1`, `""`, `" A"`, `"A "`, `"_A"`, `"é"`, `"a\n"`, `"`+strings.Repeat("a", 65)+`"`)
		}
		for _, v := range values {
			fields := map[string]string{"account_id": "1", "item_code": `"A"`, "quantity": "1"}
			fields[key] = v
			invalid = append(invalid, fmt.Sprintf(`{"account_id":%s,"item_code":%s,"quantity":%s}`, fields["account_id"], fields["item_code"], fields["quantity"]))
		}
	}
	invalid = append(invalid, `{"account_id":1,"quantity":1}`, `{"item_code":"A","quantity":1}`, `{"account_id":1,"item_code":"A"}`, `{"account_id":1,"item_code":"A","quantity":1000}`)
	for _, extra := range []string{`,"id":1`, `,"created_at":"x"`, `,"unknown":1`} {
		invalid = append(invalid, payload[:len(payload)-1]+extra+"}")
	}
	invalid = append(invalid, strings.Replace(payload, "SKU-001", string([]byte{0xff}), 1))
	for _, body := range invalid {
		call("POST", p.base+"/api/orders", body, "application/json", 400)
	}
	call("POST", p.base+"/api/orders", strings.Repeat(" ", 16385), "application/json", 413)
	call("POST", p.base+"/api/orders", payload, "text/plain", 415)
	for _, id := range []string{"0", "-1", "01", "+1", "1e0", "1.0", "9223372036854775808", "abc"} {
		call("GET", p.base+"/api/orders/"+id, "", "", 400)
	}
	for _, method := range []string{"PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"} {
		_, h := call(method, p.base+path, "", "", 405)
		if h.Get("Allow") != "GET" {
			t.Fatal("Allow")
		}
	}
	call("GET", p.base+"/unknown", "", "", 404)
	if count() != n {
		t.Fatal("invalid request wrote data")
	}
	t.Logf("real HTTP invalid bodies=%d; invalid IDs=8; all rejected without writes", len(invalid))
	for range 2 {
		old := p.cmd.Process.Pid
		stop(p)
		migrate()
		p = start()
		if p.cmd.Process.Pid == old || p.cmd.Process.Pid == initialPID {
			t.Fatal("PID did not change")
		}
		same(p.base)
	}
	stop(p)
	orders.Stop()
	orders.Restart()
	migrate()
	p = start()
	same(p.base)
	var afterHistory string
	if orderPool.QueryRow(ctx, `SELECT json_agg(h ORDER BY version)::text FROM order_schema_migrations h`).Scan(&afterHistory) != nil || afterHistory != history {
		t.Fatal("Order migration history changed")
	}
	if snapshot() != before || acct.Ready(ctx) != nil {
		t.Fatal("Account sentinel/history changed")
	}
	// Repeat Account CRUD after all Order migrations and restarts.
	after := createAccount("Regression", "regression@example.invalid")
	afterPath := accountHTTP.URL + fmt.Sprintf("/api/accounts/%d", after.ID)
	call("GET", accountHTTP.URL+"/api/accounts", "", "", 200)
	call("GET", afterPath, "", "", 200)
	call("PUT", afterPath, `{"name":"Regression Updated","email":"regression@example.invalid"}`, "application/json", 200)
	call("DELETE", afterPath, "", "", 204)
	if snapshot() != before {
		t.Fatal("Account preservation failed")
	}
	orders.Stop()
	call("GET", p.base+"/readyz", "", "", 503)
	call("GET", p.base+path, "", "", 503)
	call("GET", p.base+"/livez", "", "", 200)
	stop(p)
	t.Log("API create/get, nonexistent account, deleted account retention, two process restarts with changed PIDs, DB restart with same fresh volume, migration no-op, shutdown readiness and offline 503 passed")
	t.Log("Account CRUD before/after, duplicate 409, missing 404, validation 400, readiness, sentinel all columns and version/applied_at history unchanged passed")
}
