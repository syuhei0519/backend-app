package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/syuhei0519/backend-app/internal/account"
	"github.com/syuhei0519/backend-app/internal/ordertest"
	"github.com/syuhei0519/backend-app/internal/schemahistory"
)

var errE1Conflict = errors.New("E1 compatibility values conflict")

type e1Writer struct{ pool *pgxpool.Pool }

func (w e1Writer) create(ctx context.Context, name, email string) (int64, error) {
	var id int64
	err := w.pool.QueryRow(ctx, `INSERT INTO accounts(name,email,name_e1) VALUES($1,$2,$1) RETURNING id`, name, email).Scan(&id)
	return id, compatibilityError(err)
}

func (w e1Writer) update(ctx context.Context, id int64, name, email string) error {
	tag, err := w.pool.Exec(ctx, `UPDATE accounts SET name=$2,name_e1=$2,email=$3,updated_at=now() WHERE id=$1`, id, name, email)
	if err != nil {
		return compatibilityError(err)
	}
	if tag.RowsAffected() != 1 {
		return account.ErrNotFound
	}
	return nil
}

func (w e1Writer) delete(ctx context.Context, id int64) error {
	tag, err := w.pool.Exec(ctx, `DELETE FROM accounts WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return account.ErrNotFound
	}
	return nil
}

func (w e1Writer) read(ctx context.Context, id int64) (string, string, error) {
	var legacy, expanded string
	var oldOnly bool
	err := w.pool.QueryRow(ctx, `SELECT name,COALESCE(name_e1,''),name_e1 IS NULL FROM accounts WHERE id=$1`, id).Scan(&legacy, &expanded, &oldOnly)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", account.ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	if oldOnly {
		return legacy, "old-only", nil
	}
	if legacy != expanded {
		return "", "conflict", errE1Conflict
	}
	return expanded, "dual-written", nil
}

func compatibilityError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return account.ErrConflict
	}
	return err
}

// applyTestE1 is deliberately test-only. No migration 2 SQL is embedded in the
// production binary. The fixture records version 2 so the production C0 gates
// must reject readiness and migration while data-path compatibility is measured.
func applyTestE1(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `ALTER TABLE accounts ADD COLUMN name_e1 VARCHAR(100)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES(2)`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func requireState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, want schemahistory.State) {
	t.Helper()
	if got := schemahistory.Inspect(ctx, pool, "public.schema_migrations"); got != want {
		t.Fatalf("schema history state=%q want %q", got, want)
	}
}

func TestDisposableAccountE1CompatibilityMatrix(t *testing.T) {
	db := ordertest.Start(t, "accounts", "account_app")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, db.URL())
	if err != nil {
		t.Fatal("open E1 compatibility fixture")
	}
	defer pool.Close()
	legacy := &Store{pool: pool}
	if err = legacy.Migrate(ctx); err != nil {
		t.Fatal("create C0 schema")
	}
	requireState(t, ctx, pool, schemahistory.C0)

	evidence := map[string]bool{}
	oldRow, err := legacy.Create(ctx, account.Input{Name: "old-row", Email: "old-row@example.test"})
	if err != nil {
		t.Fatal("C0 schema / C0 writer create")
	}
	if got, err := legacy.Get(ctx, oldRow.ID); err != nil || got.Name != "old-row" {
		t.Fatal("C0 schema / C0 reader read")
	}
	evidence["c0-schema/c0-writer/c0-reader"] = true

	writer := e1Writer{pool: pool}
	if _, err = writer.create(ctx, "must-not-write", "before-expand@example.test"); err == nil {
		t.Fatal("C1 writer unexpectedly accepted C0 schema")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42703" {
			t.Fatalf("C1 writer C0 schema error=%v", err)
		}
	}
	var rows int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM accounts`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("rejected C1 write changed C0 data")
	}
	evidence["c0-schema/c1-writer/rejected-zero-write"] = true

	if err = applyTestE1(ctx, pool); err != nil {
		t.Fatal("apply test-only E1 expand")
	}
	requireState(t, ctx, pool, schemahistory.Expand)
	if schemahistory.C0Ready(schemahistory.Expand) || schemahistory.C0MigrationAllowed(schemahistory.Expand) {
		t.Fatal("production C0 gate accepted E1 history")
	}
	if err = legacy.Ready(ctx); err == nil {
		t.Fatal("production C0 readiness accepted E1")
	}
	if err = legacy.Migrate(ctx); err == nil {
		t.Fatal("production C0 migration accepted E1")
	}

	if value, shape, err := writer.read(ctx, oldRow.ID); err != nil || value != "old-row" || shape != "old-only" {
		t.Fatalf("C1 reader old row=(%q,%q,%v)", value, shape, err)
	}
	evidence["e1-schema/c0-writer/c1-reader-fallback"] = true

	// A failed backfill transaction must not expose a partial row. A later full
	// retry and an idempotent rerun complete with the old representation intact.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("begin synthetic partial backfill")
	}
	if _, err = tx.Exec(ctx, `UPDATE accounts SET name_e1=name WHERE id=$1`, oldRow.ID); err != nil {
		t.Fatal("write synthetic partial backfill")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO accounts(no_such_column) VALUES(1)`); err == nil {
		t.Fatal("synthetic backfill failure did not fail")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal("rollback synthetic partial backfill")
	}
	var oldOnly bool
	if err = pool.QueryRow(ctx, `SELECT name_e1 IS NULL FROM accounts WHERE id=$1`, oldRow.ID).Scan(&oldOnly); err != nil || !oldOnly {
		t.Fatal("partial backfill survived rollback")
	}
	tag, err := pool.Exec(ctx, `UPDATE accounts SET name_e1=name WHERE name_e1 IS NULL`)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("restartable backfill affected=%d err=%v", tag.RowsAffected(), err)
	}
	tag, err = pool.Exec(ctx, `UPDATE accounts SET name_e1=name WHERE name_e1 IS NULL`)
	if err != nil || tag.RowsAffected() != 0 {
		t.Fatalf("backfill rerun affected=%d err=%v", tag.RowsAffected(), err)
	}

	newID, err := writer.create(ctx, "new-row", "new-row@example.test")
	if err != nil {
		t.Fatal("E1 schema / C1 dual writer create")
	}
	if got, err := legacy.Get(ctx, newID); err != nil || got.Name != "new-row" {
		t.Fatal("fixed C0 SQL could not read C1 dual-written row")
	}
	if value, shape, err := writer.read(ctx, newID); err != nil || value != "new-row" || shape != "dual-written" {
		t.Fatalf("C1 reader new row=(%q,%q,%v)", value, shape, err)
	}
	evidence["e1-schema/c1-writer/c0-data-reader"] = true

	if err = writer.update(ctx, oldRow.ID, "c1-updated", "old-row@example.test"); err != nil {
		t.Fatal("C1 dual update old row")
	}
	if got, err := legacy.Get(ctx, oldRow.ID); err != nil || got.Name != "c1-updated" {
		t.Fatal("C0 data reader missed C1 update")
	}
	if _, err = legacy.Update(ctx, oldRow.ID, account.Input{Name: "c0-diverged", Email: "old-row@example.test"}); err != nil {
		t.Fatal("C0 mixed writer update")
	}
	if _, shape, err := writer.read(ctx, oldRow.ID); !errors.Is(err, errE1Conflict) || shape != "conflict" {
		t.Fatalf("C1 reader did not reject divergent row: shape=%q err=%v", shape, err)
	}
	if err = writer.update(ctx, oldRow.ID, "reconciled", "old-row@example.test"); err != nil {
		t.Fatal("C1 repair dual update")
	}

	lateOld, err := legacy.Create(ctx, account.Input{Name: "late-old", Email: "late-old@example.test"})
	if err != nil {
		t.Fatal("C0 writer during E1")
	}
	if value, shape, err := writer.read(ctx, lateOld.ID); err != nil || value != "late-old" || shape != "old-only" {
		t.Fatalf("C1 reader mixed old writer row=(%q,%q,%v)", value, shape, err)
	}
	if _, err = legacy.Create(ctx, account.Input{Name: "duplicate", Email: "new-row@example.test"}); !errors.Is(err, account.ErrConflict) {
		t.Fatalf("C0 writer duplicate error=%v", err)
	}
	if _, err = writer.create(ctx, "duplicate", "new-row@example.test"); !errors.Is(err, account.ErrConflict) {
		t.Fatalf("C1 writer duplicate error=%v", err)
	}
	if err = legacy.Delete(ctx, newID); err != nil {
		t.Fatal("C0 delete C1 row")
	}
	if _, _, err = writer.read(ctx, newID); !errors.Is(err, account.ErrNotFound) {
		t.Fatal("C1 reader observed C0-deleted row")
	}
	if err = writer.delete(ctx, lateOld.ID); err != nil {
		t.Fatal("C1 delete C0 row")
	}
	if _, err = legacy.Get(ctx, lateOld.ID); !errors.Is(err, account.ErrNotFound) {
		t.Fatal("C0 reader observed C1-deleted row")
	}

	for _, cell := range []string{
		"c0-schema/c0-writer/c0-reader",
		"c0-schema/c1-writer/rejected-zero-write",
		"e1-schema/c0-writer/c1-reader-fallback",
		"e1-schema/c1-writer/c0-data-reader",
	} {
		if !evidence[cell] {
			t.Fatalf("matrix cell unverified: %s", cell)
		}
	}
	t.Log("test-only E1 mixed CRUD, dual-write, old/new/conflict rows, backfill rollback/rerun and four matrix cells passed; production C0 remained unready")
}
