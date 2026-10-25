package notificationpostgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/syuhei0519/backend-app/internal/notification"
	"github.com/syuhei0519/backend-app/internal/ordertest"
)

func TestDisposableNotificationMigration(t *testing.T) {
	db := ordertest.Start(t, "notifications", "notification_app")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, err := Open(ctx, db.URL())
	if err != nil {
		t.Fatal("open Notification fixture")
	}
	defer s.Close()
	if s.Ready(ctx) == nil {
		t.Fatal("unmigrated Notification reported ready")
	}
	if err = s.Migrate(ctx); err != nil {
		if errors.Is(err, notification.ErrUnavailable) {
			t.Fatal("Notification migration rejected schema history")
		}
		t.Fatal("Notification migration failed")
	}
	if err = s.Ready(ctx); err != nil {
		t.Fatal("migrated Notification not ready")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("Notification no-op migration failed")
	}
}
