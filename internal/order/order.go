// Order のデータと業務上の入力条件を定義し、HTTP や DB に依存しない処理をまとめます。
// Package order defines immutable orders. Creation checks Account existence;
// stored AccountID is historical and does not prove ownership or authorization.
package order

import (
	"context"
	"errors"
	"regexp"
	"time"
)

var (
	ErrAccountUnconfigured = errors.New("account unconfigured")
	ErrAccountNotFound     = errors.New("account not found")
	ErrAccountUnavailable  = errors.New("account unavailable")
	ErrAccountTimeout      = errors.New("account timeout")
	ErrAccountBadResponse  = errors.New("invalid account response")
	ErrInvalid             = errors.New("invalid order")
	ErrNotFound            = errors.New("order not found")
	ErrUnavailable         = errors.New("order store unavailable")
	ErrInternal            = errors.New("order store failed")
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
type AccountChecker interface {
	Check(context.Context, int64) error
}
type Service struct {
	store    Store
	accounts AccountChecker
}

func New(store Store, accounts AccountChecker) *Service {
	return &Service{store: store, accounts: accounts}
}

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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.accounts == nil {
		return Order{}, ErrAccountUnconfigured
	}
	if err := s.accounts.Check(ctx, input.AccountID); err != nil {
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
