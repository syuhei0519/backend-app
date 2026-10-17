// Package notification defines immutable fake deliveries. OrderID is historical.
package notification

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	ErrInvalid     = errors.New("invalid notification")
	ErrNotFound    = errors.New("notification not found")
	ErrConflict    = errors.New("notification idempotency conflict")
	ErrUnavailable = errors.New("notification store unavailable")
	ErrInternal    = errors.New("notification store failed")
)

type Input struct {
	RequestID      string `json:"request_id"`
	OrderID        int64  `json:"order_id"`
	Recipient      string `json:"recipient"`
	Message        string `json:"message"`
	SimulateResult string `json:"simulate_result"`
}
type Notification struct {
	ID int64 `json:"id"`
	Input
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Create returns created=false only for an exact, valid replay.
type Store interface {
	Create(context.Context, Input) (Notification, bool, error)
	Get(context.Context, int64) (Notification, error)
}
type Service struct{ store Store }

func New(store Store) *Service { return &Service{store: store} }

var recipientPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func Validate(in Input) error {
	id, err := uuid.Parse(in.RequestID)
	if err != nil || id == uuid.Nil || id.String() != in.RequestID || in.OrderID < 1 || !recipientPattern.MatchString(in.Recipient) || !utf8.ValidString(in.Message) || strings.ContainsRune(in.Message, 0) || len(in.Message) == 0 || len(in.Message) > 1024 || (in.SimulateResult != "success" && in.SimulateResult != "failure") {
		return ErrInvalid
	}
	return nil
}
func Result(in Input) string {
	if in.SimulateResult == "success" {
		return "sent"
	}
	return "failed"
}
func (s *Service) Create(ctx context.Context, in Input) (Notification, bool, error) {
	if err := Validate(in); err != nil {
		return Notification{}, false, err
	}
	return s.store.Create(ctx, in)
}
func (s *Service) Get(ctx context.Context, id int64) (Notification, error) {
	if id < 1 {
		return Notification{}, ErrInvalid
	}
	return s.store.Get(ctx, id)
}
