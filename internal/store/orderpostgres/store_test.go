package orderpostgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gitlab.com/platform-engineering-lab/backend-app/internal/order"
	"testing"
)

func TestTargetConfiguration(t *testing.T) {
	for _, url := range []string{"postgres://order_app@127.0.0.1/accounts", "postgres://account_app@127.0.0.1/orders", "postgres://postgres@127.0.0.1/orders"} {
		if s, e := Open(context.Background(), url); s != nil || !errors.Is(e, ErrTarget) {
			t.Fatal("wrong target accepted")
		}
	}
}
func TestErrorClassification(t *testing.T) {
	for _, tt := range []struct{ in, want error }{{pgx.ErrNoRows, order.ErrNotFound}, {context.DeadlineExceeded, order.ErrUnavailable}, {&pgconn.PgError{Code: "57P01", Message: "private"}, order.ErrUnavailable}, {&pgconn.PgError{Code: "08006"}, order.ErrUnavailable}, {&pgconn.PgError{Code: "23514"}, order.ErrInternal}, {errors.New("private"), order.ErrInternal}} {
		if classify(tt.in) != tt.want {
			t.Fatal("classification")
		}
	}
}
