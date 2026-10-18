package paymentpostgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syuhei0519/backend-app/internal/payment"
	"testing"
)

func TestTargetConfiguration(t *testing.T) {
	for _, url := range []string{"postgres://payment_app@127.0.0.1/accounts", "postgres://account_app@127.0.0.1/payments", "postgres://postgres@127.0.0.1/payments"} {
		if s, e := Open(context.Background(), url); s != nil || !errors.Is(e, ErrTarget) {
			t.Fatal("wrong target accepted")
		}
	}
}
func TestErrorClassification(t *testing.T) {
	for _, tt := range []struct{ in, want error }{{pgx.ErrNoRows, payment.ErrNotFound}, {context.DeadlineExceeded, payment.ErrUnavailable}, {&pgconn.PgError{Code: "57P01", Message: "private"}, payment.ErrUnavailable}, {&pgconn.PgError{Code: "08006"}, payment.ErrUnavailable}, {&pgconn.PgError{Code: "23514"}, payment.ErrInternal}, {errors.New("private"), payment.ErrInternal}} {
		if classify(tt.in) != tt.want {
			t.Fatal("classification")
		}
	}
}

type identityRow struct {
	db, user string
	err      error
}

func (r identityRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = r.db
	*dest[1].(*string) = r.user
	return nil
}

type identityQuery struct{ row identityRow }

func (q identityQuery) QueryRow(context.Context, string, ...any) pgx.Row { return q.row }
func TestActualConnectionGuard(t *testing.T) {
	for _, tt := range []struct {
		db, user string
		want     error
	}{
		{"payments", "payment_app", nil}, {"orders", "payment_app", ErrTarget}, {"payments", "postgres", ErrTarget}, {"accounts", "account_app", ErrTarget},
	} {
		if err := guard(context.Background(), identityQuery{identityRow{db: tt.db, user: tt.user}}); err != tt.want {
			t.Fatal("actual connection identity guard")
		}
	}
	if err := guard(context.Background(), identityQuery{identityRow{err: context.DeadlineExceeded}}); err != payment.ErrUnavailable {
		t.Fatal("guard failure classification")
	}
}
