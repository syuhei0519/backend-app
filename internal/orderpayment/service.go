// Package orderpayment coordinates one payment request without changing Order state.
package orderpayment

import (
	"context"
	"errors"
	"time"

	"github.com/syuhei0519/backend-app/internal/order"
	"github.com/syuhei0519/backend-app/internal/payment"
)

const RouteTimeout = 5 * time.Second

var ErrUnconfigured = errors.New("payment unconfigured")

type Getter interface {
	Get(context.Context, int64) (order.Order, error)
}
type Creator interface {
	Create(context.Context, payment.Input) (payment.Payment, bool, error)
}
type Service struct {
	orders   Getter
	payments Creator
}

func New(orders Getter, payments Creator) *Service {
	return &Service{orders: orders, payments: payments}
}

func (s *Service) Create(ctx context.Context, in payment.Input) (payment.Payment, bool, error) {
	if err := payment.Validate(in); err != nil {
		return payment.Payment{}, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, RouteTimeout)
	defer cancel()
	if _, err := s.orders.Get(ctx, in.OrderID); err != nil {
		return payment.Payment{}, false, err
	}
	if s.payments == nil {
		return payment.Payment{}, false, ErrUnconfigured
	}
	return s.payments.Create(ctx, in)
}
