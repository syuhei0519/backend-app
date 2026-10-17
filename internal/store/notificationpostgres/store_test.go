package notificationpostgres

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"gitlab.com/platform-engineering-lab/backend-app/internal/notification"
	"testing"
)

func TestTargetConfiguration(t *testing.T) {
	for _, url := range []string{"postgres://notification_app@127.0.0.1/accounts", "postgres://account_app@127.0.0.1/notifications", "postgres://postgres@127.0.0.1/notifications"} {
		if s, e := Open(context.Background(), url); s != nil || !errors.Is(e, ErrTarget) {
			t.Fatal("wrong target accepted")
		}
	}
}
func TestErrorClassification(t *testing.T) {
	for _, tt := range []struct{ in, want error }{{pgx.ErrNoRows, notification.ErrNotFound}, {context.DeadlineExceeded, notification.ErrUnavailable}, {&pgconn.PgError{Code: "57P01", Message: "private"}, notification.ErrUnavailable}, {&pgconn.PgError{Code: "08006"}, notification.ErrUnavailable}, {&pgconn.PgError{Code: "23514"}, notification.ErrInternal}, {errors.New("private"), notification.ErrInternal}} {
		if classify(tt.in) != tt.want {
			t.Fatal("classification")
		}
	}
}
