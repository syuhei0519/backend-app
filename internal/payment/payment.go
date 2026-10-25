// Payment のデータと業務上の入力条件を定義し、HTTP や DB に依存しない処理をまとめます。
// Package payment defines immutable fake payment attempts.
// OrderID is a historical reference; no Order existence check is performed.
package payment

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid payment")
	ErrNotFound    = errors.New("payment not found")
	ErrConflict    = errors.New("payment idempotency conflict")
	ErrUnavailable = errors.New("payment store unavailable")
	ErrInternal    = errors.New("payment store failed")
)

type Input struct {
	RequestID      string `json:"request_id"`
	OrderID        int64  `json:"order_id"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	SimulateResult string `json:"simulate_result"`
}
type Payment struct {
	Input
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}
type Store interface {
	Create(context.Context, Input) (Payment, bool, error)
	Get(context.Context, int64) (Payment, error)
}
type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }
func Validate(in Input) error {
	id, err := uuid.Parse(in.RequestID)
	if err != nil || id == uuid.Nil || id.String() != in.RequestID || in.OrderID < 1 || in.AmountMinor < 1 || in.Currency != "JPY" || (in.SimulateResult != "success" && in.SimulateResult != "decline") {
		return ErrInvalid
	}
	return nil
}

// Result is deterministic; received exists only inside the insertion transaction.
func Result(in Input) string {
	if in.SimulateResult == "success" {
		return "succeeded"
	}
	return "declined"
}
func (s *Service) Create(ctx context.Context, in Input) (Payment, bool, error) {
	if err := Validate(in); err != nil {
		return Payment{}, false, err
	}
	return s.store.Create(ctx, in)
}
func (s *Service) Get(ctx context.Context, id int64) (Payment, error) {
	if id < 1 {
		return Payment{}, ErrInvalid
	}
	return s.store.Get(ctx, id)
}
