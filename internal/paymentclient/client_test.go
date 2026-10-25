// Payment 呼び出しの入力・期限・応答検証と、再送を避ける振る舞いを確認します。
package paymentclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syuhei0519/backend-app/internal/payment"
	"github.com/syuhei0519/backend-app/internal/paymenthttp"
)

var input = payment.Input{RequestID: "550e8400-e29b-41d4-a716-446655440000", OrderID: 1, AmountMinor: 1000, Currency: "JPY", SimulateResult: "success"}

func resource(in payment.Input) payment.Payment {
	return payment.Payment{Input: in, ID: 1, Status: payment.Result(in), CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}
func TestOrigin(t *testing.T) {
	for _, origin := range []string{"http://learning-payment:8080", "http://127.0.0.1:1234", "http://[::1]:8080", "http://payment.example:80"} {
		if ValidateOrigin(origin) != nil {
			t.Fatalf("valid origin rejected")
		}
	}
	for _, origin := range []string{"", "https://learning-payment:8080", "http://user:pass@host:80", "http://host", "http://host:0", "http://host:65536", "http://host:80/", "http://host:80/api", "http://host:80?", "http://host:80?q=1", "http://host:80#", "http://host:80#x", "http://bad_host:80", "http://-host:80", "http://[fe80::1%25eth0]:80", "http://host:+80", "http://host:80\\x"} {
		if ValidateOrigin(origin) == nil {
			t.Fatal("invalid origin accepted")
		}
	}
}
func TestResponseContract(t *testing.T) {
	raw, _ := json.Marshal(resource(input))
	valid := string(raw)
	for _, tt := range []struct {
		name        string
		status      int
		body, media string
		want        error
		created     bool
	}{
		{"created", 201, valid, "application/json", nil, true}, {"replay", 200, valid, "application/json; charset=UTF-8", nil, false},
		{"case-sensitive-extension", 201, strings.TrimSuffix(valid, "}") + `,"ID":0}`, "application/json", nil, true},
		{"extension", 201, strings.TrimSuffix(valid, "}") + `,"future":{"v":[1,true,null]}}`, "application/json", nil, true},
		{"conflict", 409, `{"error":{"code":"idempotency_conflict","message":"Conflict"}}`, "application/json", payment.ErrConflict, false},
		{"unavailable", 503, `{"error":{"code":"unavailable","message":"Service Unavailable"}}`, "application/json", ErrUnavailable, false},
		{"bad-error", 503, `{"error":{"code":"private","message":"private"}}`, "application/json", ErrBadResponse, false},
		{"invalid-input-from-provider", 400, `{}`, "application/json", ErrBadResponse, false},
		{"redirect", 307, valid, "application/json", ErrBadResponse, false}, {"server-error", 500, valid, "application/json", ErrBadResponse, false},
		{"media", 201, valid, "text/plain", ErrBadResponse, false}, {"oversize", 201, valid + strings.Repeat(" ", MaxResponse), "application/json", ErrBadResponse, false},
		{"trailing", 201, valid + `{}`, "application/json", ErrBadResponse, false}, {"duplicate", 201, strings.TrimSuffix(valid, "}") + `,"id":1}`, "application/json", ErrBadResponse, false},
		{"nested-duplicate", 201, strings.TrimSuffix(valid, "}") + `,"future":{"a":1,"a":2}}`, "application/json", ErrBadResponse, false},
		{"missing", 201, strings.Replace(valid, `"id":1,`, "", 1), "application/json", ErrBadResponse, false},
		{"null", 201, strings.Replace(valid, `"id":1`, `"id":null`, 1), "application/json", ErrBadResponse, false},
		{"zero-id", 201, strings.Replace(valid, `"id":1`, `"id":0`, 1), "application/json", ErrBadResponse, false},
		{"wrong-identity", 201, strings.Replace(valid, `"order_id":1`, `"order_id":2`, 1), "application/json", ErrBadResponse, false},
		{"nonterminal", 201, strings.Replace(valid, `"succeeded"`, `"received"`, 1), "application/json", ErrBadResponse, false},
		{"wrong-status", 201, strings.Replace(valid, `"succeeded"`, `"declined"`, 1), "application/json", ErrBadResponse, false},
		{"bad-time", 201, strings.Replace(valid, `2026-01-01T00:00:00Z`, `invalid`, 1), "application/json", ErrBadResponse, false},
		{"utf8", 201, strings.TrimSuffix(valid, "}") + ",\"extra\":\"\xff\"}", "application/json", ErrBadResponse, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.URL.Path != "/api/payments" || r.Header.Get("Idempotency-Key") != "" || r.Header.Get("X-Idempotency-Key") != "" {
					t.Error("unexpected wire request")
				}
				w.Header().Set("Content-Type", tt.media)
				w.Header().Set("Location", "http://private.invalid")
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			c, _ := New(server.URL)
			defer c.Close()
			_, created, err := c.Create(context.Background(), input)
			if !errors.Is(err, tt.want) || created != tt.created || calls.Load() != 1 {
				t.Fatalf("err=%v created=%v calls=%d", err, created, calls.Load())
			}
		})
	}
}
func TestDeadlinesAndCancellation(t *testing.T) {
	for _, mode := range []string{"headers", "body", "parent-cancel", "parent-deadline"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			started := make(chan struct{})
			release := make(chan struct{})
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				close(started)
				if mode == "body" {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(201)
					w.(http.Flusher).Flush()
					fmt.Fprint(w, `{"id":`)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer s.Close()
			defer close(release)
			c, _ := New(s.URL)
			defer c.Close()
			ctx := context.Background()
			want := ErrTimeout
			if mode == "parent-cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				go func() { <-started; cancel() }()
				want = ErrUnavailable
			}
			if mode == "parent-deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			start := time.Now()
			_, _, err := c.Create(ctx, input)
			elapsed := time.Since(start)
			if !errors.Is(err, want) || calls.Load() != 1 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
			if mode == "headers" || mode == "body" {
				if elapsed < Timeout || elapsed > Timeout+time.Second {
					t.Fatalf("elapsed %s", elapsed)
				}
				t.Logf("deadline elapsed=%s wire_POST=%d", elapsed, calls.Load())
			} else if elapsed > time.Second {
				t.Fatal("parent cancellation ignored")
			}
		})
	}
}
func TestTransportAndProxyBoundary(t *testing.T) {
	var proxyCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1) }))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	c, _ := New("http://" + address)
	defer c.Close()
	_, _, err = c.Create(context.Background(), input)
	if !errors.Is(err, ErrUnavailable) || proxyCalls.Load() != 0 {
		t.Fatal("transport/proxy boundary")
	}
}

type providerStore struct{ item payment.Payment }

func (s *providerStore) Create(_ context.Context, in payment.Input) (payment.Payment, bool, error) {
	if s.item.ID != 0 {
		if s.item.Input != in {
			return payment.Payment{}, false, payment.ErrConflict
		}
		return s.item, false, nil
	}
	s.item = resource(in)
	return s.item, true, nil
}
func (s *providerStore) Get(context.Context, int64) (payment.Payment, error) { return s.item, nil }
func (s *providerStore) Ready(context.Context) error                         { return nil }
func TestExistingProviderContract(t *testing.T) {
	for _, result := range []string{"success", "decline"} {
		t.Run(result, func(t *testing.T) {
			store := &providerStore{}
			s := httptest.NewServer(paymenthttp.New(payment.New(store), store))
			defer s.Close()
			c, _ := New(s.URL)
			defer c.Close()
			in := input
			in.SimulateResult = result
			for i := 0; i < 2; i++ {
				p, created, err := c.Create(context.Background(), in)
				if err != nil || created != (i == 0) || p.Status != payment.Result(in) || p.Input != in {
					t.Fatal("provider contract")
				}
			}
			in.AmountMinor++
			if _, _, err := c.Create(context.Background(), in); !errors.Is(err, payment.ErrConflict) {
				t.Fatal("conflict")
			}
		})
	}
}

func TestResetDoesNotResendPOST(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error("fixture hijack")
			return
		}
		_ = conn.Close()
	}))
	defer s.Close()
	c, _ := New(s.URL)
	defer c.Close()
	_, _, err := c.Create(context.Background(), input)
	if !errors.Is(err, ErrUnavailable) || calls.Load() != 1 {
		t.Fatalf("reset err=%v wire_POST=%d", err, calls.Load())
	}
}
