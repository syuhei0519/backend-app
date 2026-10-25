// Order API の入力・応答・稼働確認を、差し替えた保存処理で確認します。
package orderhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/syuhei0519/backend-app/internal/order"
)

type fakeStore struct {
	calls int
	err   error
}

func (f *fakeStore) Create(_ context.Context, in order.Input) (order.Order, error) {
	f.calls++
	return order.Order{ID: 1, AccountID: in.AccountID, ItemCode: in.ItemCode, Quantity: in.Quantity, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, f.err
}
func (f *fakeStore) Get(_ context.Context, id int64) (order.Order, error) {
	f.calls++
	return order.Order{ID: id}, f.err
}
func (f *fakeStore) Ready(context.Context) error { return f.err }

const valid = `{"account_id":1,"item_code":"SKU-001","quantity":2}`

func request(s *Server, method, path, body, media string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if media != "" {
		r.Header.Set("Content-Type", media)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestInputBoundary(t *testing.T) {
	bad := []string{"", "null", "[]", "1", "true", `"str"`, `{}`, valid + ` {}`, valid + ` x`, valid[:len(valid)-1], `{"account_id":1,"account_id":2,"item_code":"A","quantity":1}`, `{"account_id":1,"\u0061ccount_id":2,"item_code":"A","quantity":1}`}
	for _, key := range []string{"account_id", "item_code", "quantity"} {
		for _, value := range []string{"null", "{}", "[]", "true"} {
			parts := map[string]string{"account_id": "1", "item_code": `"A"`, "quantity": "1"}
			parts[key] = value
			bad = append(bad, fmt.Sprintf(`{"account_id":%s,"item_code":%s,"quantity":%s}`, parts["account_id"], parts["item_code"], parts["quantity"]))
		}
	}
	for _, key := range []string{"account_id", "quantity"} {
		for _, v := range []string{`"1"`, "1.0", "1e0", "-1", "0", "9223372036854775808"} {
			bad = append(bad, strings.Replace(valid, `"`+key+`":`+map[string]string{"account_id": "1", "quantity": "2"}[key], `"`+key+`":`+v, 1))
		}
	}
	for _, v := range []string{`""`, `" A"`, `"A "`, `"_A"`, `"a\n"`, `"é"`, `1`, `"` + strings.Repeat("a", 65) + `"`} {
		bad = append(bad, strings.Replace(valid, `"SKU-001"`, v, 1))
	}
	for _, extra := range []string{`,"id":1`, `,"created_at":"x"`, `,"unknown":0`} {
		bad = append(bad, valid[:len(valid)-1]+extra+"}")
	}
	bad = append(bad, strings.Replace(valid, `"quantity":2`, `"quantity":1000`, 1), strings.Replace(valid, `"account_id":1,`, "", 1), strings.Replace(valid, `"item_code":"SKU-001",`, "", 1), strings.Replace(valid, `,"quantity":2`, "", 1), strings.Replace(valid, "SKU-001", string([]byte{0xff}), 1))
	for i, body := range bad {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := &fakeStore{}
			w := request(New(order.New(f, f), f), "POST", "/api/orders", body, "application/json")
			if w.Code != 400 || f.calls != 0 || !strings.Contains(w.Body.String(), `"validation_error"`) {
				t.Fatalf("status=%d calls=%d", w.Code, f.calls)
			}
		})
	}
}
func TestLimitsAndMedia(t *testing.T) {
	for _, tt := range []struct {
		body, media string
		status      int
	}{{valid, "application/json", 201}, {valid, "application/json; charset=utf-8", 201}, {valid, "application/json; charset=UTF-8", 201}, {valid, "", 415}, {valid, "text/plain", 415}, {valid, "application/json; charset=latin1", 415}, {valid, "application/json; other=x", 415}, {valid + strings.Repeat(" ", MaxBody-len(valid)), "application/json", 201}, {valid + strings.Repeat(" ", MaxBody-len(valid)+1), "application/json", 413}} {
		f := &fakeStore{}
		w := request(New(order.New(f, f), f), "POST", "/api/orders", tt.body, tt.media)
		if w.Code != tt.status {
			t.Fatalf("status %d want %d", w.Code, tt.status)
		}
		if tt.status != 201 && f.calls != 0 {
			t.Fatal("invalid called store")
		}
		if tt.status == 201 && w.Header().Get("Location") != "/api/orders/1" {
			t.Fatal("missing Location")
		}
	}
}
func TestRoutesAndErrors(t *testing.T) {
	for _, tt := range []struct {
		method, path string
		status       int
		allow        string
	}{{"GET", "/api/orders/1", 200, ""}, {"GET", "/api/orders/9223372036854775807", 200, ""}, {"GET", "/api/orders/01", 400, ""}, {"GET", "/api/orders/+1", 400, ""}, {"GET", "/api/orders/0", 400, ""}, {"GET", "/api/orders/-1", 400, ""}, {"GET", "/api/orders/9223372036854775808", 400, ""}, {"GET", "/api/orders/1.0", 400, ""}, {"GET", "/api/orders/1e0", 400, ""}, {"GET", "/api/orders", 405, "POST"}, {"HEAD", "/api/orders/1", 405, "GET"}, {"POST", "/api/orders/1", 405, "GET"}, {"DELETE", "/api/orders/1", 405, "GET"}, {"GET", "/api/orders/", 404, ""}, {"GET", "/api/orders/1/x", 404, ""}, {"GET", "//api/orders/1", 404, ""}, {"GET", "/unknown", 404, ""}, {"GET", "/livez", 200, ""}, {"POST", "/readyz", 405, "GET"}} {
		f := &fakeStore{}
		w := request(New(order.New(f, f), f), tt.method, tt.path, "", "")
		if w.Code != tt.status || w.Header().Get("Allow") != tt.allow {
			t.Fatalf("%s %s: %d %s", tt.method, tt.path, w.Code, w.Header().Get("Allow"))
		}
		if tt.status >= 400 && f.calls != 0 {
			t.Fatal("invalid route reached store")
		}
	}
	for _, tt := range []struct {
		err    error
		status int
	}{{order.ErrNotFound, 404}, {order.ErrUnavailable, 503}, {order.ErrInternal, 500}, {errors.New("private sql dsn"), 500}} {
		f := &fakeStore{err: tt.err}
		w := request(New(order.New(f, f), f), "GET", "/api/orders/1", "", "")
		if w.Code != tt.status || strings.Contains(w.Body.String(), "private") {
			t.Fatal("unsafe error mapping")
		}
	}
	f := &fakeStore{}
	s := New(order.New(f, f), f)
	if request(s, "GET", "/readyz", "", "").Code != 200 {
		t.Fatal("ready")
	}
	s.BeginShutdown()
	if request(s, "GET", "/readyz", "", "").Code != 503 || request(s, "GET", "/livez", "", "").Code != 200 {
		t.Fatal("shutdown health")
	}
}

func (f *fakeStore) Check(context.Context, int64) error { return nil }
