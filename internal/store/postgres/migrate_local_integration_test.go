package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/ordertest"
)

// TestDisposableAccountMigration is the mandatory local Account DB vector.
// The older operator-supplied acceptance test remains a labelled compatibility test.
func TestDisposableAccountMigration(t *testing.T) {
	db := ordertest.Start(t, "accounts", "account_app")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, db.URL())
	if err != nil {
		t.Fatal("open Account fixture")
	}
	defer pool.Close()
	s := &Store{pool: pool}
	if s.Ready(ctx) == nil {
		t.Fatal("unmigrated Account reported ready")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("fresh Account migration")
	}
	if err = s.Ready(ctx); err != nil {
		t.Fatal("migrated Account not ready")
	}
	var before string
	if err = pool.QueryRow(ctx, `SELECT json_agg(h ORDER BY version)::text FROM schema_migrations h`).Scan(&before); err != nil {
		t.Fatal("read Account history")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("Account no-op migration")
	}
	var after string
	if err = pool.QueryRow(ctx, `SELECT json_agg(h ORDER BY version)::text FROM schema_migrations h`).Scan(&after); err != nil || after != before {
		t.Fatal("Account no-op changed history")
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire Account fixture")
	}
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	err = applyMigration(short, conn, nil, "schema_migrations", 2, `CREATE TABLE partial_piece(id int); SELECT pg_sleep(10)`)
	stop()
	conn.Release()
	if err == nil {
		t.Fatal("Account interrupted migration succeeded")
	}
	var partial bool
	if err = pool.QueryRow(ctx, `SELECT to_regclass('partial_piece') IS NOT NULL OR EXISTS(SELECT 1 FROM schema_migrations WHERE version=2)`).Scan(&partial); err != nil || partial {
		t.Fatal("Account partial DDL/history survived")
	}
	db.Stop()
	if err = s.Ready(ctx); err == nil {
		t.Fatal("offline Account reported ready")
	}
	t.Log("Account fresh/no-op migration, interrupted rollback, and offline readiness passed")
}
