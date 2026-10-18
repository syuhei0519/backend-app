package orderhttp

import (
	"context"
	"errors"
	"fmt"
	"github.com/syuhei0519/backend-app/internal/order"
	"github.com/syuhei0519/backend-app/internal/payment"
	"github.com/syuhei0519/backend-app/internal/paymentclient"
	"strings"
	"testing"
)

const validPayment = `{"request_id":"550e8400-e29b-41d4-a716-446655440000","amount_minor":1000,"currency":"JPY","simulate_result":"success"}`

type fakePayment struct {
	calls   int
	in      payment.Input
	err     error
	created bool
}

func (f *fakePayment) Create(_ context.Context, in payment.Input) (payment.Payment, bool, error) {
	f.calls++
	f.in = in
	return payment.Payment{Input: in, ID: 9, Status: payment.Result(in)}, f.created, f.err
}
func TestPaymentInputBoundary(t *testing.T) {
	bad := []string{"", "null", "[]", "{}", validPayment + `{}`, validPayment + `x`, validPayment[:len(validPayment)-1], strings.Replace(validPayment, "JPY", string([]byte{0xff}), 1)}
	values := map[string]string{"request_id": `"550e8400-e29b-41d4-a716-446655440000"`, "amount_minor": "1000", "currency": `"JPY"`, "simulate_result": `"success"`}
	for key, v := range values {
		for _, wrong := range []string{"null", "{}", "[]", "true", "1.0"} {
			bad = append(bad, strings.Replace(validPayment, `"`+key+`":`+v, `"`+key+`":`+wrong, 1))
		}
		bad = append(bad, strings.Replace(validPayment, `"`+key+`":`+v, `"`+key+`":`+v+`,"`+key+`":`+v, 1))
		bad = append(bad, strings.Replace(validPayment, `"`+key+`":`+v, `"`+key+`":`+v+`,"\u`+fmt.Sprintf("%04x", key[0])+key[1:]+`":`+v, 1))
	}
	for _, extra := range []string{`,"order_id":1`, `,"unknown":1`, `,"status":"succeeded"`} {
		bad = append(bad, strings.TrimSuffix(validPayment, "}")+extra+"}")
	}
	for _, v := range []string{"0", "-1", "1e3", "9223372036854775808", `"1000"`} {
		bad = append(bad, strings.Replace(validPayment, "1000", v, 1))
	}
	for _, v := range []string{"00000000-0000-0000-0000-000000000000", "550E8400-E29B-41D4-A716-446655440000", "invalid"} {
		bad = append(bad, strings.Replace(validPayment, "550e8400-e29b-41d4-a716-446655440000", v, 1))
	}
	bad = append(bad, strings.Replace(validPayment, "JPY", "USD", 1), strings.Replace(validPayment, "success", "declined", 1))
	for i, body := range bad {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			f := &fakeStore{}
			p := &fakePayment{}
			w := request(NewWithPayments(order.New(f), f, p), "POST", "/api/orders/7/payments", body, "application/json")
			if w.Code != 400 || f.calls != 0 || p.calls != 0 {
				t.Fatalf("status=%d order=%d payment=%d", w.Code, f.calls, p.calls)
			}
		})
	}
}
func TestPaymentRoutesAndErrors(t *testing.T) {
	for _, tt := range []struct {
		method, path, body, media string
		status                    int
		allow                     string
	}{
		{"POST", "/api/orders/7/payments", validPayment, "application/json", 201, ""},
		{"POST", "/api/orders/7/payments", validPayment, "application/json; charset=utf-8", 201, ""},
		{"POST", "/api/orders/7/payments", validPayment, "", 415, ""},
		{"POST", "/api/orders/7/payments", validPayment + strings.Repeat(" ", MaxBody-len(validPayment)+1), "application/json", 413, ""},
		{"POST", "/api/orders/7/payments", validPayment + strings.Repeat(" ", MaxBody-len(validPayment)), "application/json", 201, ""},
		{"GET", "/api/orders/7/payments", validPayment, "application/json", 405, "POST"},
		{"HEAD", "/api/orders/7/payments", validPayment, "application/json", 405, "POST"},
		{"POST", "/api/orders/01/payments", validPayment, "application/json", 400, ""},
		{"POST", "/api/orders/0/payments", validPayment, "application/json", 400, ""},
		{"POST", "/api/orders/+1/payments", validPayment, "application/json", 400, ""},
		{"POST", "/api/orders/9223372036854775808/payments", validPayment, "application/json", 400, ""},
		{"POST", "/api/orders/1/payments/", validPayment, "application/json", 404, ""},
		{"POST", "/api/orders/1/x/payments", validPayment, "application/json", 404, ""},
	} {
		f := &fakeStore{}
		p := &fakePayment{created: true}
		w := request(NewWithPayments(order.New(f), f, p), tt.method, tt.path, tt.body, tt.media)
		if w.Code != tt.status || w.Header().Get("Allow") != tt.allow {
			t.Fatalf("%s %s status=%d", tt.method, tt.path, w.Code)
		}
		if tt.status >= 400 && (f.calls != 0 || p.calls != 0) {
			t.Fatal("invalid reached dependencies")
		}
		if tt.status == 201 && (p.in.OrderID != 7 || w.Header().Get("Location") != "") {
			t.Fatal("path identity or Location")
		}
	}
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{payment.ErrConflict, 409, "idempotency_conflict"}, {paymentclient.ErrTimeout, 504, "payment_timeout"}, {paymentclient.ErrUnavailable, 503, "payment_unavailable"}, {paymentclient.ErrBadResponse, 502, "payment_bad_response"}, {errors.New("private sql url"), 500, "internal_error"},
	} {
		f := &fakeStore{}
		p := &fakePayment{err: tt.err}
		w := request(NewWithPayments(order.New(f), f, p), "POST", "/api/orders/1/payments", validPayment, "application/json")
		if w.Code != tt.status || !strings.Contains(w.Body.String(), `"`+tt.code+`"`) || strings.Contains(w.Body.String(), "private") {
			t.Fatal("unsafe error")
		}
	}
	for _, err := range []error{order.ErrNotFound, order.ErrUnavailable, order.ErrInternal} {
		f := &fakeStore{err: err}
		p := &fakePayment{}
		_ = request(NewWithPayments(order.New(f), f, p), "POST", "/api/orders/1/payments", validPayment, "application/json")
		if p.calls != 0 {
			t.Fatal("failed Order called Payment")
		}
	}
	f := &fakeStore{}
	s := New(order.New(f), f)
	if w := request(s, "POST", "/api/orders/1/payments", validPayment, "application/json"); w.Code != 503 || !strings.Contains(w.Body.String(), "payment_unconfigured") {
		t.Fatal("unconfigured")
	}
	if request(s, "GET", "/readyz", "", "").Code != 200 || request(s, "POST", "/api/orders", valid, "application/json").Code != 201 {
		t.Fatal("legacy API affected")
	}
	p := &fakePayment{}
	if request(NewWithPayments(order.New(f), f, p), "POST", "/api/orders/1/payments", validPayment, "application/json").Code != 200 {
		t.Fatal("replay status")
	}
}
