package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/syuhei0519/backend-app/internal/runtimeprofile"
)

type migrationIdentityRow struct {
	database string
	role     string
	owner    string
	err      error
}

func (r migrationIdentityRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*string)) = r.database
	*(dest[1].(*string)) = r.role
	*(dest[2].(*string)) = r.owner
	return nil
}

type migrationGuardFixture struct {
	row        migrationIdentityRow
	query      string
	readCount  int
	writeCount int
	events     []string
}

func (f *migrationGuardFixture) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	f.query = sql
	f.readCount++
	f.events = append(f.events, "read")
	return f.row
}

func (f *migrationGuardFixture) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.writeCount++
	f.events = append(f.events, sql)
	return pgconn.CommandTag{}, nil
}

func TestMigrationIdentityRejectionPerformsNoWrite(t *testing.T) {
	profile := &runtimeprofile.Profile{
		DatabaseName:  "account",
		DatabaseRole:  "account_app",
		DatabaseOwner: "account_app",
	}
	for _, test := range []struct {
		name string
		row  migrationIdentityRow
	}{
		{"wrong-database", migrationIdentityRow{database: "other", role: "account_app", owner: "account_app"}},
		{"wrong-role", migrationIdentityRow{database: "account", role: "other", owner: "account_app"}},
		{"wrong-owner", migrationIdentityRow{database: "account", role: "account_app", owner: "unexpected_owner"}},
		{"query-failure", migrationIdentityRow{err: errors.New("fixture unavailable")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := &migrationGuardFixture{row: test.row}
			locked, err := prepareMigration(context.Background(), fixture, profile)
			if err == nil {
				t.Fatal("invalid database identity accepted")
			}
			if locked || fixture.writeCount != 0 {
				t.Fatalf("identity rejection reached a write: locked=%v writes=%d", locked, fixture.writeCount)
			}
			if fixture.readCount != 1 || !strings.Contains(fixture.query, "pg_database") || strings.Contains(fixture.query, "schema_migrations") {
				t.Fatalf("identity guard query = %q reads=%d", fixture.query, fixture.readCount)
			}
		})
	}
}

func TestMigrationIdentityProofPrecedesLockAndDDL(t *testing.T) {
	profile := &runtimeprofile.Profile{DatabaseName: "account", DatabaseRole: "account_app", DatabaseOwner: "account_app"}
	fixture := &migrationGuardFixture{row: migrationIdentityRow{database: "account", role: "account_app", owner: "account_app"}}
	locked, err := prepareMigration(context.Background(), fixture, profile)
	if err != nil || !locked {
		t.Fatalf("migration preparation failed: locked=%v err=%v", locked, err)
	}
	if fixture.readCount != 1 || fixture.writeCount != 2 {
		t.Fatalf("preparation reads=%d writes=%d", fixture.readCount, fixture.writeCount)
	}
	if len(fixture.events) != 3 || fixture.events[0] != "read" || !strings.Contains(fixture.events[1], "pg_advisory_lock") || !strings.Contains(fixture.events[2], "CREATE TABLE") {
		t.Fatalf("migration preparation order = %q", fixture.events)
	}
}
