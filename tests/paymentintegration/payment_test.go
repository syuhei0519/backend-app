package paymentintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gitlab.com/platform-engineering-lab/backend-app/internal/payment"
	"gitlab.com/platform-engineering-lab/backend-app/internal/paymenttest"
)

// One DB/API lifecycle, including one API process restart. Opt-in only.
func TestDisposablePaymentLifecycle(t *testing.T) {
	paymenttest.Enabled(t)
	db := paymenttest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, db.URL())
	if err != nil {
		t.Fatal("fixture pool")
	}
	defer pool.Close()
	binary := filepath.Join(t.TempDir(), "payment-server")
	if exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/payment-server").Run() != nil {
		t.Fatal("build Payment")
	}
	env := func(address string) []string {
		return []string{"PATH=" + os.Getenv("PATH"), "DATABASE_URL=" + db.URL(), "PAYMENT_HTTP_ADDRESS=" + address, "LOG_LEVEL=error"}
	}
	migrate := func() {
		t.Helper()
		c := exec.CommandContext(ctx, binary, "migrate")
		c.Env = env("127.0.0.1:0")
		if c.Run() != nil {
			t.Fatal("owned migration")
		}
	}
	migrate()
	history := func() string {
		t.Helper()
		var s string
		if pool.QueryRow(ctx, "SELECT json_agg(h ORDER BY version)::text FROM public.payment_schema_migrations h").Scan(&s) != nil {
			t.Fatal("history")
		}
		return s
	}
	before := history()
	migrate()
	if history() != before {
		t.Fatal("migration no-op changed timestamp")
	}
	var names string
	if pool.QueryRow(ctx, `SELECT string_agg(tablename,',' ORDER BY tablename) FROM pg_tables WHERE schemaname='public'`).Scan(&names) != nil || names != "payment_schema_migrations,payments" {
		t.Fatal("owned migration boundary")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	type process struct {
		cmd     *exec.Cmd
		done    chan error
		base    string
		stopped bool
	}
	start := func() *process {
		t.Helper()
		l, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal("HTTP port")
		}
		address := l.Addr().String()
		_ = l.Close()
		p := &process{cmd: exec.CommandContext(ctx, binary), done: make(chan error, 1), base: "http://" + address}
		p.cmd.Env = env(address)
		if p.cmd.Start() != nil {
			t.Fatal("start Payment")
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
			r, e := client.Get(p.base + "/readyz")
			if e == nil {
				r.Body.Close()
				if r.StatusCode == 200 {
					return p
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("Payment not ready")
		return nil
	}
	stop := func(p *process) {
		t.Helper()
		if p.cmd.Process.Signal(syscall.SIGTERM) != nil {
			t.Fatal("signal Payment")
		}
		if e := <-p.done; e != nil {
			t.Fatal("stop Payment")
		}
		p.stopped = true
	}
	// call has no testing mutations so concurrent request checks can use it safely.
	type response struct {
		raw      []byte
		status   int
		location string
		err      error
	}
	call := func(method, url, body string) response {
		req, e := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
		if e != nil {
			return response{err: e}
		}
		req.Header.Set("Content-Type", "application/json")
		r, e := client.Do(req)
		if e != nil {
			return response{err: e}
		}
		defer r.Body.Close()
		raw, e := io.ReadAll(r.Body)
		return response{raw, r.StatusCode, r.Header.Get("Location"), e}
	}
	check := func(r response, status int) {
		t.Helper()
		if r.err != nil || r.status != status {
			t.Fatalf("HTTP wanted %d got %d; transport failed=%t", status, r.status, r.err != nil)
		}
	}
	body := func(id, result string) string {
		return fmt.Sprintf(`{"request_id":%q,"order_id":9223372036854775807,"amount_minor":1000,"currency":"JPY","simulate_result":%q}`, id, result)
	}
	p := start()
	type saved struct {
		body string
		r    response
	}
	var accepted []saved
	for n, result := range []string{"success", "decline"} {
		payload := body(fmt.Sprintf("550e8400-e29b-41d4-a716-44665544000%d", n), result)
		r := call("POST", p.base+"/api/payments", payload)
		check(r, 201)
		var item payment.Payment
		if json.Unmarshal(r.raw, &item) != nil || item.ID < 1 || item.OrderID != 9223372036854775807 || item.CreatedAt.IsZero() || item.Status != payment.Result(item.Input) {
			t.Fatal("terminal result")
		}
		if r.location != fmt.Sprintf("/api/payments/%d", item.ID) {
			t.Fatal("Location")
		}
		got := call("GET", p.base+r.location, "")
		check(got, 200)
		if !bytes.Equal(got.raw, r.raw) {
			t.Fatal("GET representation")
		}
		replay := call("POST", p.base+"/api/payments", payload)
		check(replay, 200)
		if !bytes.Equal(replay.raw, r.raw) || replay.location != r.location {
			t.Fatal("sequential replay")
		}
		// Typed equality ignores whitespace/key ordering.
		// Preserve int64 spelling rather than re-marshalling through float64.
		reordered := fmt.Sprintf(`{ "simulate_result":%q,"currency":"JPY","amount_minor":1000,"order_id":9223372036854775807,"request_id":%q }`, result, item.RequestID)
		replay = call("POST", p.base+"/api/payments", reordered)
		check(replay, 200)
		if !bytes.Equal(replay.raw, r.raw) {
			t.Fatal("semantic replay")
		}
		for _, change := range []string{strings.Replace(payload, "1000", "1001", 1), strings.Replace(payload, "9223372036854775807", "1", 1), strings.Replace(payload, result, map[string]string{"success": "decline", "decline": "success"}[result], 1)} {
			check(call("POST", p.base+"/api/payments", change), 409)
		}
		accepted = append(accepted, saved{payload, r})
	}
	// Race new keys, including conflicting inputs: one 201, replays 200, conflicts 409.
	for n, result := range []string{"success", "decline"} {
		payload := body(fmt.Sprintf("550e8400-e29b-41d4-a716-44665544001%d", n), result)
		replies := make(chan response, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				b := payload
				if i%2 == 1 {
					b = strings.Replace(b, "1000", "1001", 1)
				}
				replies <- call("POST", p.base+"/api/payments", b)
			}(i)
		}
		wg.Wait()
		close(replies)
		created, conflicts := 0, 0
		var winner response
		for r := range replies {
			if r.err != nil {
				t.Fatal("concurrent transport")
			}
			switch r.status {
			case 201:
				created++
				winner = r
			case 200:
			case 409:
				conflicts++
			default:
				t.Fatal("concurrent status", r.status)
			}
		}
		if created != 1 || conflicts != 4 {
			t.Fatal("concurrent uniqueness")
		}
		var item payment.Payment
		_ = json.Unmarshal(winner.raw, &item)
		b, _ := json.Marshal(item.Input)
		accepted = append(accepted, saved{string(b), winner})
	}
	var count int
	if pool.QueryRow(ctx, "SELECT count(*) FROM public.payments").Scan(&count) != nil || count != 4 {
		t.Fatal("one row per accepted key")
	}
	for _, b := range []string{"{}", strings.Replace(accepted[0].body, "1000", "1.0", 1), accepted[0].body + "{}"} {
		check(call("POST", p.base+"/api/payments", b), 400)
	}
	check(call("GET", p.base+"/api/payments/9223372036854775807", ""), 404)
	pid := p.cmd.Process.Pid
	stop(p)
	migrate()
	p = start()
	if p.cmd.Process.Pid == pid {
		t.Fatal("API PID unchanged")
	}
	for _, a := range accepted {
		r := call("GET", p.base+a.r.location, "")
		check(r, 200)
		if !bytes.Equal(r.raw, a.r.raw) {
			t.Fatal("restart GET")
		}
		r = call("POST", p.base+"/api/payments", a.body)
		check(r, 200)
		if !bytes.Equal(r.raw, a.r.raw) {
			t.Fatal("restart replay")
		}
	}
	if history() != before {
		t.Fatal("migration history changed")
	}
	db.Stop()
	check(call("GET", p.base+"/readyz", ""), 503)
	check(call("GET", p.base+accepted[0].r.location, ""), 503)
	check(call("POST", p.base+"/api/payments", accepted[0].body), 503)
	check(call("GET", p.base+"/livez", ""), 200)
	stop(p)
	t.Log("single disposable Payment lifecycle: terminal results, sequential/concurrent/conflicting replays, API restart, migration ownership/no-op and DB-unavailable checked")
}
