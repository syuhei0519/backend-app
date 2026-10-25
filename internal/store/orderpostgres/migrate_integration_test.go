// 使い捨ての Order 用 DB で、migration の繰り返しと失敗時の扱いを確認する任意実行テストです。
package orderpostgres

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/order"
	"github.com/syuhei0519/backend-app/internal/ordertest"
)

func TestDisposableMigration(t *testing.T) {
	d := ordertest.Start(t, "orders", "order_app")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, err := Open(ctx, d.URL())
	if err != nil {
		t.Fatal("open")
	}
	defer s.Close()
	if s.Ready(ctx) == nil {
		t.Fatal("unmigrated ready")
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("fresh migration")
	}
	if err = s.Ready(ctx); err != nil {
		t.Fatal("migrated not ready")
	}
	history := func() string {
		var result string
		if s.pool.QueryRow(ctx, `SELECT json_agg(h ORDER BY version)::text FROM public.order_schema_migrations h`).Scan(&result) != nil {
			t.Fatal("history")
		}
		return result
	}
	before := history()
	errorsCh := make(chan error, 2)
	for range 2 {
		go func() { errorsCh <- s.Migrate(ctx) }()
	}
	for range 2 {
		if <-errorsCh != nil {
			t.Fatal("concurrent no-op failed")
		}
	}
	if history() != before {
		t.Fatal("no-op history changed")
	}
	blocker, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal("blocker")
	}
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		t.Fatal("lock")
	}
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	err = s.Migrate(short)
	stop()
	if err == nil {
		t.Fatal("lock wait did not cancel")
	}
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_unlock($1)`, lockID); err != nil {
		t.Fatal("unlock")
	}
	blocker.Release()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("retry after lock cancellation")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire")
	}
	short, stop = context.WithTimeout(ctx, 100*time.Millisecond)
	err = applyMigration(short, conn, 2, `CREATE TABLE public.partial_piece(id int); SELECT pg_sleep(10)`)
	stop()
	conn.Release()
	if err == nil {
		t.Fatal("DDL interruption not observed")
	}
	var exists bool
	if s.pool.QueryRow(ctx, `SELECT to_regclass('public.partial_piece') IS NOT NULL OR EXISTS(SELECT 1 FROM public.order_schema_migrations WHERE version=2)`).Scan(&exists) != nil || exists {
		t.Fatal("partial DDL/history persisted")
	}
	conn, err = s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal("retry acquire")
	}
	err = applyMigration(ctx, conn, 2, `CREATE TABLE public.partial_piece(id int)`)
	conn.Release()
	if err != nil {
		t.Fatal("DDL retry")
	}
	if s.Ready(ctx) == nil || s.Migrate(ctx) == nil {
		t.Fatal("future version accepted")
	}
	t.Log("fresh/no-op/concurrent migration, lock cancellation/recovery, DDL/history atomic rollback/retry, future version rejection passed")
}
func TestDisposableTargetGuard(t *testing.T) {
	d := ordertest.Start(t, "orders", "order_app")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Bypass the configuration check only inside this same-package test to prove
	// the actual migration connection guard rejects both incorrect DB and role.
	wrongDB, _ := url.Parse(d.URL())
	wrongDB.Path = "/postgres"
	for _, target := range []string{wrongDB.String(), d.AdminURL("orders")} {
		pool, err := pgxpool.New(ctx, target)
		if err != nil {
			t.Fatal("test pool")
		}
		s := &Store{pool: pool}
		if !errors.Is(s.Migrate(ctx), ErrTarget) {
			pool.Close()
			t.Fatal("actual target guard failed")
		}
		var exists bool
		if pool.QueryRow(ctx, `SELECT to_regclass('public.order_schema_migrations') IS NOT NULL`).Scan(&exists) != nil || exists {
			pool.Close()
			t.Fatal("wrong target was written")
		}
		pool.Close()
	}
	s, err := Open(ctx, d.URL())
	if err != nil {
		t.Fatal("open")
	}
	defer s.Close()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal("migrate")
	}
	for _, sql := range []string{`INSERT INTO public.orders(account_id,item_code,quantity) VALUES(0,'A',1)`, `INSERT INTO public.orders(account_id,item_code,quantity) VALUES(1,'é',1)`, `INSERT INTO public.orders(account_id,item_code,quantity) VALUES(1,'A',1000)`, `INSERT INTO public.orders(id,account_id,item_code,quantity) VALUES(99,1,'A',1)`} {
		if _, err = s.pool.Exec(ctx, sql); err == nil {
			t.Fatal("SQL constraint missing")
		}
	}
	if _, err = s.Create(ctx, order.Input{AccountID: 9223372036854775807, ItemCode: "A", Quantity: 999}); err != nil {
		t.Fatal("valid max boundary failed")
	}
	d.Stop()
	if err = s.Ready(ctx); err == nil {
		t.Fatal("offline ready")
	}
	if _, err = s.Get(ctx, 1); !errors.Is(err, order.ErrUnavailable) {
		t.Fatal("offline mapping")
	}
	t.Log("wrong DB/role rejected before DDL; SQL constraints and offline classification passed")
}
