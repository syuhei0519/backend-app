package orderpaymentintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gitlab.com/platform-engineering-lab/backend-app/internal/order"
	"gitlab.com/platform-engineering-lab/backend-app/internal/payment"
)

func port(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal("fixture listener")
	}
	a := l.Addr().String()
	l.Close()
	return a
}
func build(t *testing.T, service string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), service)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", p, "./cmd/"+service+"-server")
	cmd.Dir = "../.."
	if cmd.Run() != nil {
		t.Fatal("build fixture API")
	}
	return p
}
func env(d *Database, address, paymentURL string) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=/tmp", "DATABASE_URL=" + d.URL(), "ORDER_HTTP_ADDRESS=" + address, "PAYMENT_HTTP_ADDRESS=" + address, "ORDER_PAYMENT_URL=" + paymentURL, "LOG_LEVEL=error"}
}
func migrate(t *testing.T, binary string, d *Database) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "migrate")
	cmd.Env = env(d, "127.0.0.1:0", "")
	if cmd.Run() != nil {
		t.Fatal("fixture migration failed")
	}
}
func start(t *testing.T, binary string, d *Database, address, paymentURL string) func() {
	t.Helper()
	cmd := exec.Command(binary)
	cmd.Env = env(d, address, paymentURL)
	if cmd.Start() != nil {
		t.Fatal("start fixture API")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	stop := func() { once.Do(func() { _ = cmd.Process.Kill(); <-done }) }
	t.Cleanup(stop)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		r, e := client.Get("http://" + address + "/readyz")
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				return stop
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("fixture API readiness")
	return nil
}

type reply struct {
	status   int
	item     payment.Payment
	location string
}

func send(base, path string, body any) (reply, error) {
	raw, _ := json.Marshal(body)
	res, e := http.Post(base+path, "application/json", bytes.NewReader(raw))
	if e != nil {
		return reply{}, fmt.Errorf("request failed")
	}
	defer res.Body.Close()
	var p payment.Payment
	_ = json.NewDecoder(res.Body).Decode(&p)
	return reply{res.StatusCode, p, res.Header.Get("Location")}, nil
}
func must(t *testing.T, base, path string, body any, status int) payment.Payment {
	t.Helper()
	r, e := send(base, path, body)
	if e != nil || r.status != status || r.location != "" {
		t.Fatalf("payment status=%d want=%d", r.status, status)
	}
	return r.item
}
func count(t *testing.T, d *Database, id string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, e := pgx.Connect(ctx, d.URL())
	if e != nil {
		t.Fatal("fixture row-count connection")
	}
	defer c.Close(ctx)
	var n int
	if c.QueryRow(ctx, "SELECT count(*) FROM public.payments WHERE request_id=$1", id).Scan(&n) != nil {
		t.Fatal("fixture row count")
	}
	return n
}
func TestOrderPaymentLifecycle(t *testing.T) {
	Enabled(t)
	odb := Start(t, "orders", "order_app")
	pdb := Start(t, "payments", "payment_app")
	ob, pb := build(t, "order"), build(t, "payment")
	migrate(t, ob, odb)
	migrate(t, pb, pdb)
	migrate(t, ob, odb)
	pa, oa := port(t), port(t)
	start(t, pb, pdb, pa, "")
	// Fixture-only relay counts wire POSTs and can drop a committed response. Production has no proxy.
	var calls atomic.Int32
	var drop atomic.Bool
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		req, e := http.NewRequestWithContext(r.Context(), "POST", "http://"+pa+"/api/payments", r.Body)
		if e != nil {
			t.Error("relay request")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Error("relay upstream")
			return
		}
		defer res.Body.Close()
		body, e := io.ReadAll(res.Body)
		if e != nil {
			t.Error("relay body")
			return
		}
		if drop.Swap(false) {
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		_, _ = w.Write(body)
	}))
	defer relay.Close()
	stop := start(t, ob, odb, oa, relay.URL)
	base := "http://" + oa
	// Existing create/get behavior with the real Order process.
	raw := `{"account_id":1,"item_code":"SKU-PAY","quantity":1}`
	res, e := http.Post(base+"/api/orders", "application/json", strings.NewReader(raw))
	if e != nil {
		t.Fatal("create Order")
	}
	var o order.Order
	_ = json.NewDecoder(res.Body).Decode(&o)
	res.Body.Close()
	if res.StatusCode != 201 || o.ID < 1 {
		t.Fatal("create Order result")
	}
	r, e := http.Get(fmt.Sprintf("%s/api/orders/%d", base, o.ID))
	if e != nil {
		t.Fatal("get Order")
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("legacy get")
	}
	path := fmt.Sprintf("/api/orders/%d/payments", o.ID)
	in := map[string]any{"request_id": uuid.NewString(), "amount_minor": 1000, "currency": "JPY", "simulate_result": "success"}
	before := calls.Load()
	missing := fmt.Sprintf("/api/orders/%d/payments", o.ID+999)
	must(t, base, missing, in, 404)
	bad := map[string]any{"request_id": in["request_id"], "order_id": o.ID, "amount_minor": 1000, "currency": "JPY", "simulate_result": "success"}
	must(t, base, path, bad, 400)
	if calls.Load() != before || count(t, pdb, in["request_id"].(string)) != 0 {
		t.Fatal("invalid/missing Order called Payment")
	}
	first := must(t, base, path, in, 201)
	second := must(t, base, path, in, 200)
	if first.ID != second.ID || !first.CreatedAt.Equal(second.CreatedAt) || first.Status != "succeeded" {
		t.Fatal("sequential replay identity")
	}
	decline := map[string]any{"request_id": uuid.NewString(), "amount_minor": 1000, "currency": "JPY", "simulate_result": "decline"}
	declined := must(t, base, path, decline, 201)
	if declined.Status != "declined" || must(t, base, path, decline, 200).Status != "declined" {
		t.Fatal("decline changed")
	}
	changed := map[string]any{"request_id": in["request_id"], "amount_minor": 1001, "currency": "JPY", "simulate_result": "success"}
	must(t, base, path, changed, 409)
	changed["amount_minor"] = 1000
	changed["simulate_result"] = "decline"
	must(t, base, path, changed, 409)
	res, e = http.Post(base+"/api/orders", "application/json", strings.NewReader(raw))
	if e != nil {
		t.Fatal("second Order")
	}
	var other order.Order
	_ = json.NewDecoder(res.Body).Decode(&other)
	res.Body.Close()
	if res.StatusCode != 201 || other.ID == o.ID {
		t.Fatal("second Order result")
	}
	must(t, base, fmt.Sprintf("/api/orders/%d/payments", other.ID), in, 409)

	concurrent := map[string]any{"request_id": uuid.NewString(), "amount_minor": 1000, "currency": "JPY", "simulate_result": "success"}
	results := make(chan reply, 8)
	var wg sync.WaitGroup
	before = calls.Load()
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := send(base, path, concurrent)
			if e != nil {
				r.status = 0
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	created := 0
	var winner payment.Payment
	for r := range results {
		if r.status != 200 && r.status != 201 {
			t.Fatal("parallel response")
		}
		if r.status == 201 {
			created++
		}
		if winner.ID == 0 {
			winner = r.item
		}
		if r.item.ID != winner.ID || !r.item.CreatedAt.Equal(winner.CreatedAt) || r.item.Status != winner.Status {
			t.Fatal("parallel identity")
		}
	}
	if created != 1 || calls.Load()-before != 8 || count(t, pdb, concurrent["request_id"].(string)) != 1 {
		t.Fatal("parallel duplicate or retry")
	}
	stop()
	stop = start(t, ob, odb, oa, relay.URL)
	afterRestart := must(t, base, path, in, 200)
	if afterRestart.ID != first.ID || !afterRestart.CreatedAt.Equal(first.CreatedAt) || count(t, pdb, in["request_id"].(string)) != 1 {
		t.Fatal("restart replay")
	}
	lost := map[string]any{"request_id": uuid.NewString(), "amount_minor": 1000, "currency": "JPY", "simulate_result": "success"}
	drop.Store(true)
	before = calls.Load()
	began := time.Now()
	must(t, base, path, lost, 504)
	elapsed := time.Since(began)
	if elapsed < 2*time.Second || elapsed > 3*time.Second || calls.Load()-before != 1 || count(t, pdb, lost["request_id"].(string)) != 1 {
		t.Fatal("commit response loss / deadline / wire count")
	}
	recovered := must(t, base, path, lost, 200)
	if recovered.Status != "succeeded" || count(t, pdb, lost["request_id"].(string)) != 1 {
		t.Fatal("manual recovery")
	}
	t.Logf("commit-response-loss elapsed=%s wire_POST=1 row_count=1 manual_replay=200", elapsed)
	for _, m := range []map[string]any{in, decline, concurrent, lost} {
		if count(t, pdb, m["request_id"].(string)) != 1 {
			t.Fatal("duplicate records")
		}
	}
	stop()
	start(t, ob, odb, oa, "")
	must(t, base, path, in, 503)
	t.Log("real APIs=2 disposable DBs=2 success/decline/serial+parallel+restart replay/conflict/zero-call boundaries passed; repeated migration and unset-URL readiness passed")
}
