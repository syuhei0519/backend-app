// Order 作成時の Account 確認について、存在しない場合や通信失敗時の HTTP 応答を確認します。
package orderhttp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syuhei0519/backend-app/internal/accountclient"
	"github.com/syuhei0519/backend-app/internal/order"
)

func TestAccountLookupHTTPContract(t *testing.T) {
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	const requestID = "550e8400-e29b-41d4-a716-446655440000"
	for _, tt := range []struct {
		name, body         string
		downstream, status int
		code               string
		delay, slowBody    bool
	}{
		{name: "valid", body: `{"id":1,"name":"unused","email":"unused"}`, downstream: 200, status: 201},
		{name: "missing", downstream: 404, status: 422, code: "account_not_found"},
		{name: "5xx", downstream: 500, status: 503, code: "account_unavailable"},
		{name: "503", downstream: 503, status: 503, code: "account_unavailable"},
		{name: "headers timeout", status: 504, code: "account_timeout", delay: true},
		{name: "body timeout", downstream: 200, status: 504, code: "account_timeout", slowBody: true},
		{name: "redirect", downstream: 302, status: 502, code: "account_bad_response"},
		{name: "wrong id", body: `{"id":2}`, downstream: 200, status: 502, code: "account_bad_response"},
		{name: "duplicate id", body: `{"id":1,"id":1}`, downstream: 200, status: 502, code: "account_bad_response"},
		{name: "malformed", body: `{`, downstream: 200, status: 502, code: "account_bad_response"},
		{name: "trailing", body: `{"id":1} {}`, downstream: 200, status: 502, code: "account_bad_response"},
		{name: "oversized", body: `{"id":1,"extra":"` + strings.Repeat("x", accountclient.MaxResponse) + `"}`, downstream: 200, status: 502, code: "account_bad_response"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "GET" || r.URL.Path != "/api/accounts/1" {
					t.Error("lookup target")
				}
				if r.Header.Get("Traceparent") != traceparent || r.Header.Get("Tracestate") != "vendor=value" || r.Header.Get("X-Request-ID") != requestID {
					t.Error("context not propagated")
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("Baggage") != "" {
					t.Error("sensitive headers forwarded")
				}
				if tt.delay {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Location", "/redirect-must-not-follow")
				w.WriteHeader(tt.downstream)
				if tt.slowBody {
					fmt.Fprint(w, `{"id":`)
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				fmt.Fprint(w, tt.body)
			}))
			defer downstream.Close()
			checker, err := accountclient.New(downstream.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer checker.Close()
			f := &fakeStore{}
			upstream := httptest.NewServer(New(order.New(f, checker), f))
			defer upstream.Close()
			req, _ := http.NewRequest("POST", upstream.URL+"/api/orders", strings.NewReader(valid))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Traceparent", traceparent)
			req.Header.Set("Tracestate", "vendor=value")
			req.Header.Set("X-Request-ID", requestID)
			req.Header.Set("Authorization", "test-value-must-not-forward")
			req.Header.Set("Baggage", "private=test")
			began := time.Now()
			res, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
			if err != nil {
				t.Fatal("upstream request failed")
			}
			defer res.Body.Close()
			if res.StatusCode != tt.status || res.Header.Get("X-Request-ID") != requestID {
				t.Fatalf("status=%d want=%d", res.StatusCode, tt.status)
			}
			if tt.code != "" {
				b, _ := io.ReadAll(res.Body)
				if !strings.Contains(string(b), `"code":"`+tt.code+`"`) {
					t.Fatal("error code")
				}
			}
			elapsed := time.Since(began)
			if tt.delay || tt.slowBody {
				if elapsed < 1800*time.Millisecond || elapsed > 3*time.Second {
					t.Fatalf("deadline elapsed=%s", elapsed)
				}
			}
			writes := 0
			if tt.status == 201 {
				writes = 1
			}
			if f.calls != writes || calls.Load() != 1 {
				t.Fatalf("store=%d downstream=%d", f.calls, calls.Load())
			}
			t.Logf("HTTP=%d code=%s downstream=1 writes=%d elapsed=%s", res.StatusCode, tt.code, writes, elapsed.Round(time.Millisecond))
		})
	}
}

func TestAccountConnectionFailureAndParentCancellation(t *testing.T) {
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closed.Close()
	c, _ := accountclient.New(closed.URL)
	defer c.Close()
	f := &fakeStore{}
	if w := request(New(order.New(f, c), f), "POST", "/api/orders", valid, "application/json"); w.Code != 503 || !strings.Contains(w.Body.String(), "account_unavailable") || f.calls != 0 {
		t.Fatal("connection failure")
	}
	if w := request(New(order.New(f, nil), f), "POST", "/api/orders", valid, "application/json"); w.Code != 503 || !strings.Contains(w.Body.String(), "account_unconfigured") {
		t.Fatal("unconfigured bypass")
	}
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer downstream.Close()
	c2, _ := accountclient.New(downstream.URL)
	defer c2.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := c2.Check(ctx, 1); err != order.ErrAccountTimeout || time.Since(started) > time.Second {
		t.Fatal("parent deadline not respected")
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if err := c2.Check(ctx, 1); err != order.ErrAccountUnavailable {
		t.Fatal("parent cancellation not propagated")
	}
}
func TestInvalidOrderDoesNotLookup(t *testing.T) {
	var calls atomic.Int32
	downstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer downstream.Close()
	c, _ := accountclient.New(downstream.URL)
	defer c.Close()
	f := &fakeStore{}
	for _, body := range []string{`{}`, `{"account_id":0,"item_code":"A","quantity":1}`, `{"account_id":1,"item_code":"A","quantity":1000}`} {
		if w := request(New(order.New(f, c), f), "POST", "/api/orders", body, "application/json"); w.Code != 400 {
			t.Fatal("validation")
		}
	}
	if calls.Load() != 0 || f.calls != 0 {
		t.Fatal("invalid input reached dependency")
	}
}
