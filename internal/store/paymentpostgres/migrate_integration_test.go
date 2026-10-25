package paymentpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/syuhei0519/backend-app/internal/ordertest"
	"github.com/syuhei0519/backend-app/internal/payment"
)

func TestDisposablePaymentMigration(t *testing.T) {
	db := ordertest.Start(t, "payments", "payment_app")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, err := Open(ctx, db.URL())
	if err != nil {
		t.Fatal("open Payment fixture")
	}
	defer s.Close()
	if s.Ready(ctx) == nil {
		t.Fatal("unmigrated Payment reported ready")
	}
	if err = s.Migrate(ctx); err != nil {
		if errors.Is(err, payment.ErrUnavailable) {
			t.Fatal("Payment migration rejected schema history")
		}
		t.Fatal("Payment migration failed")
	}
	if err = s.Ready(ctx); err != nil {
		t.Fatal("migrated Payment not ready")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("Payment no-op migration failed")
	}
}
