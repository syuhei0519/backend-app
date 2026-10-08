// 明示指定された隔離DBだけを使う移行統合試験。未指定時はSkipする。
// 適用済み履歴の冪等性・lock競合timeout・SQL途中失敗時の原子性を確認し、作成した専用schemaのみ回収する。
// このコメント作業ではDB資格情報を用意せず、本試験を実DBへ接続して実行しない。
package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Opt-in, disposable-cluster only. The DSN is supplied in memory by the operator.
// 誤操作防止に専用context名とloopback port-forwardを要求する。
// 後始末はdeferで試みるが同じ30秒ctxを使うため、期限超過時のDROP成功までは保証しない。
func TestMigrationDisposableDatabase(t *testing.T) {
	url := os.Getenv("PE_MIGRATION_TEST_URL")
	if url == "" {
		t.Skip("isolated database not supplied")
	}
	if os.Getenv("PE_ACCEPTANCE_TARGET") != "kind-core-platform-at0102" {
		t.Fatal("wrong acceptance target")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid private test configuration")
	}
	if cfg.ConnConfig.Host != "127.0.0.1" || cfg.ConnConfig.Port != 18083 {
		t.Fatal("expected isolated port-forward")
	}
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("create test admin pool failed")
	}
	defer admin.Close()
	const schema = "pe002b_migration_acceptance"
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("test schema must be absent", err)
	}
	defer func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error("test schema cleanup failed", err)
		}
	}()
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.MaxConns = 5
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal("create migration pool failed")
	}
	defer pool.Close()
	store := &Store{pool: pool}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal("initial migration failed", err)
	}
	var first time.Time
	if err = pool.QueryRow(ctx, "SELECT applied_at FROM schema_migrations WHERE version=1").Scan(&first); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal("idempotent migration failed", err)
	}
	var count int
	var second time.Time
	if err = pool.QueryRow(ctx, "SELECT count(*),min(applied_at) FROM schema_migrations").Scan(&count, &second); err != nil || count != 1 || !first.Equal(second) {
		t.Fatalf("history changed: count=%d err=%v", count, err)
	}

	blocker, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Release()
	if _, err = blocker.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		t.Fatal(err)
	}
	blocked, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	err = store.Migrate(blocked)
	stop()
	if err == nil {
		t.Fatal("contended advisory lock did not time out")
	}
	if _, err = blocker.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockID); err != nil {
		t.Fatal(err)
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal("retry after contention failed", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	interrupted, halt := context.WithTimeout(ctx, 100*time.Millisecond)
	err = applyMigration(interrupted, conn, 2, "CREATE TABLE interrupted_piece(id integer); SELECT pg_sleep(10)")
	halt()
	if err == nil {
		t.Fatal("SQL interruption was not observed")
	}
	// 中断で物理sessionが破棄され得るので、新しい接続を使う。
	conn.Release()
	conn, err = pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var table *string
	if err = conn.QueryRow(ctx, "SELECT to_regclass('interrupted_piece')::text").Scan(&table); err != nil || table != nil {
		t.Fatalf("partial DDL survived: err=%v", err)
	}
	if err = conn.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version=2").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial history survived: err=%v", err)
	}
	if err = applyMigration(ctx, conn, 2, "CREATE TABLE interrupted_piece(id integer)"); err != nil {
		t.Fatal("SQL retry failed", err)
	}
	t.Log("idempotence, lock cancellation/recovery, interrupted DDL/history rollback and retry passed")
}
