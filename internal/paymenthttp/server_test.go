package paymenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"gitlab.com/platform-engineering-lab/backend-app/internal/payment"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeStore struct {
	calls   int
	created bool
	err     error
}

func (f *fakeStore) Create(_ context.Context, in payment.Input) (payment.Payment, bool, error) {
	f.calls++
	return payment.Payment{Input: in, ID: 1, Status: payment.Result(in)}, f.created, f.err
}
func (f *fakeStore) Get(context.Context, int64) (payment.Payment, error) {
	f.calls++
	return payment.Payment{}, f.err
}
func (f *fakeStore) Ready(context.Context) error { return f.err }

const valid = `{"request_id":"550e8400-e29b-41d4-a716-446655440000","order_id":1,"amount_minor":1000,"currency":"JPY","simulate_result":"success"}`

func request(s *Server, method, path, body, media string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", media)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestStrictInput(t *testing.T) {
	bad := []string{"", "null", "[]", "{}", valid + "{}", valid + " x", valid[:len(valid)-1], strings.Replace(valid, "JPY", string([]byte{255}), 1)}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal([]byte(valid), &fields)
	for key := range fields {
		for _, value := range []string{"null", "{}", "[]", "true"} {
			var m map[string]json.RawMessage
			_ = json.Unmarshal([]byte(valid), &m)
			m[key] = json.RawMessage(value)
			b, _ := json.Marshal(m)
			bad = append(bad, string(b))
		}
		var m map[string]json.RawMessage
		_ = json.Unmarshal([]byte(valid), &m)
		delete(m, key)
		b, _ := json.Marshal(m)
		bad = append(bad, string(b))
		bad = append(bad, valid[:len(valid)-1]+","+strconvQuote(key)+":"+string(fields[key])+"}")
	}
	for _, key := range []string{"order_id", "amount_minor"} {
		old := map[string]string{"order_id": "1", "amount_minor": "1000"}[key]
		for _, value := range []string{"0", "-1", "1.0", "1e0", `"1"`, "9223372036854775808"} {
			bad = append(bad, strings.Replace(valid, strconvQuote(key)+":"+old, strconvQuote(key)+":"+value, 1))
		}
	}
	bad = append(bad, strings.Replace(valid, `"currency"`, `"unknown"`, 1), strings.Replace(valid, `"currency":"JPY"`, `"currency":"JPY","\u0063urrency":"JPY"`, 1), strings.Replace(valid, "JPY", "USD", 1), strings.Replace(valid, "success", "failure", 1), strings.Replace(valid, "550e8400", "550E8400", 1))
	for i, b := range bad {
		f := &fakeStore{}
		w := request(New(payment.New(f), f), "POST", "/api/payments", b, "application/json")
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("invalid case %d status %d calls %d", i, w.Code, f.calls)
		}
	}
}
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
func TestCreationReplayAndErrors(t *testing.T) {
	for _, created := range []bool{true, false} {
		f := &fakeStore{created: created}
		w := request(New(payment.New(f), f), "POST", "/api/payments", valid, "application/json; charset=UTF-8")
		want := 200
		if created {
			want = 201
		}
		if w.Code != want || w.Header().Get("Location") != "/api/payments/1" {
			t.Fatal("create/replay")
		}
	}
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{payment.ErrConflict, 409, "idempotency_conflict"}, {payment.ErrNotFound, 404, "not_found"}, {payment.ErrUnavailable, 503, "unavailable"}, {errors.New("private DSN"), 500, "internal_error"},
	} {
		f := &fakeStore{err: tt.err}
		w := request(New(payment.New(f), f), "POST", "/api/payments", valid, "application/json")
		if w.Code != tt.status || !strings.Contains(w.Body.String(), tt.code) || strings.Contains(w.Body.String(), "private") {
			t.Fatal("error mapping")
		}
	}
}
func TestLimitsRoutesAndDrain(t *testing.T) {
	for _, tt := range []struct {
		body, media string
		status      int
	}{
		{valid + strings.Repeat(" ", MaxBody-len(valid)), "application/json", 201}, {strings.Repeat(" ", MaxBody+1), "application/json", 413}, {valid, "", 415}, {valid, "text/plain", 415}, {valid, "application/json; charset=latin1", 415}, {valid, "application/json; other=x", 415},
	} {
		f := &fakeStore{created: true}
		w := request(New(payment.New(f), f), "POST", "/api/payments", tt.body, tt.media)
		if w.Code != tt.status {
			t.Fatal("limit/media", w.Code)
		}
	}
	for _, tt := range []struct {
		method, path string
		status       int
		allow        string
	}{
		{"GET", "/api/payments/1", 200, ""}, {"GET", "/api/payments/01", 400, ""}, {"GET", "/api/payments/0", 400, ""}, {"GET", "/api/payments/+1", 400, ""}, {"GET", "/api/payments/9223372036854775808", 400, ""}, {"GET", "/api/payments/1e0", 400, ""}, {"GET", "/api/payments/", 404, ""}, {"GET", "/api/payments/1/x", 404, ""}, {"GET", "//api/payments/1", 404, ""}, {"GET", "/api/payments", 405, "POST"}, {"HEAD", "/api/payments/1", 405, "GET"}, {"DELETE", "/api/payments/1", 405, "GET"}, {"POST", "/readyz", 405, "GET"},
	} {
		f := &fakeStore{}
		w := request(New(payment.New(f), f), tt.method, tt.path, "", "")
		if w.Code != tt.status || w.Header().Get("Allow") != tt.allow {
			t.Fatal("route", tt.path, w.Code)
		}
	}
	f := &fakeStore{}
	s := New(payment.New(f), f)
	if request(s, "GET", "/readyz", "", "").Code != 200 {
		t.Fatal("ready")
	}
	s.BeginShutdown()
	if request(s, "GET", "/readyz", "", "").Code != 503 || request(s, "GET", "/livez", "", "").Code != 200 {
		t.Fatal("drain")
	}
}
