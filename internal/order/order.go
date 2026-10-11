// Package order defines immutable orders. AccountID is a historical reference,
// not proof of account existence, identity, or authorization.
package order

import (
	"context"
	"errors"
	"regexp"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid order")
	ErrNotFound    = errors.New("order not found")
	ErrUnavailable = errors.New("order store unavailable")
	ErrInternal    = errors.New("order store failed")
)

type Order struct {
	ID        int64     `json:"id"`
	AccountID int64     `json:"account_id"`
	ItemCode  string    `json:"item_code"`
	Quantity  int       `json:"quantity"`
	CreatedAt time.Time `json:"created_at"`
}
type Input struct {
	AccountID int64  `json:"account_id"`
	ItemCode  string `json:"item_code"`
	Quantity  int    `json:"quantity"`
}
type Store interface {
	Create(context.Context, Input) (Order, error)
	Get(context.Context, int64) (Order, error)
}
type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

var itemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func Validate(input Input) error {
	if input.AccountID < 1 || input.Quantity < 1 || input.Quantity > 999 || !itemPattern.MatchString(input.ItemCode) {
		return ErrInvalid
	}
	return nil
}
func (s *Service) Create(ctx context.Context, input Input) (Order, error) {
	if err := Validate(input); err != nil {
		return Order{}, err
	}
	return s.store.Create(ctx, input)
}
func (s *Service) Get(ctx context.Context, id int64) (Order, error) {
	if id < 1 {
		return Order{}, ErrInvalid
	}
	return s.store.Get(ctx, id)
}
