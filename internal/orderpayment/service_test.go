package orderpayment

import (
	"context"
	"errors"
	"gitlab.com/platform-engineering-lab/backend-app/internal/order"
	"gitlab.com/platform-engineering-lab/backend-app/internal/payment"
	"testing"
	"time"
)

type getter struct {
	calls int
	err   error
}

func (g *getter) Get(ctx context.Context, id int64) (order.Order, error) {
	g.calls++
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > RouteTimeout {
		panic("route deadline missing")
	}
	return order.Order{ID: id}, g.err
}

type creator struct{ calls int }

func (c *creator) Create(_ context.Context, in payment.Input) (payment.Payment, bool, error) {
	c.calls++
	return payment.Payment{Input: in}, true, nil
}
func TestOrderBeforePayment(t *testing.T) {
	valid := payment.Input{RequestID: "550e8400-e29b-41d4-a716-446655440000", OrderID: 1, AmountMinor: 1, Currency: "JPY", SimulateResult: "success"}
	for _, err := range []error{nil, order.ErrNotFound, order.ErrUnavailable, order.ErrInternal} {
		g := &getter{err: err}
		c := &creator{}
		p, created, e := New(g, c).Create(context.Background(), valid)
		if !errors.Is(e, err) || g.calls != 1 {
			t.Fatal("order error")
		}
		if err != nil && c.calls != 0 {
			t.Fatal("payment called after order failure")
		}
		if err == nil && (c.calls != 1 || !created || p.Input != valid) {
			t.Fatal("input propagation")
		}
	}
	g := &getter{}
	c := &creator{}
	bad := valid
	bad.OrderID = 0
	_, _, err := New(g, c).Create(context.Background(), bad)
	if !errors.Is(err, payment.ErrInvalid) || g.calls != 0 || c.calls != 0 {
		t.Fatal("invalid reached dependency")
	}
	_, _, err = New(g, nil).Create(context.Background(), valid)
	if !errors.Is(err, ErrUnconfigured) || g.calls != 1 {
		t.Fatal("unconfigured")
	}
}
