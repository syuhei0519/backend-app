package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/syuhei0519/backend-app/internal/runtimeprofile"
)

func TestDatabaseParseErrorDoesNotLeakInput(t *testing.T) {
	const marker = "PRIVATE_DATABASE_CANARY"
	tests := []struct {
		name     string
		profiles []*runtimeprofile.Profile
	}{
		{name: "legacy"},
		{name: "explicit", profiles: []*runtimeprofile.Profile{&runtimeprofile.Profile{Name: "explicit-fixture"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Open(context.Background(), "postgres://%"+marker, test.profiles...)
			if err == nil {
				t.Fatal("invalid database configuration accepted")
			}
			if strings.Contains(err.Error(), marker) {
				t.Fatal("database configuration leaked")
			}
			if err.Error() != errParseDatabaseConfiguration.Error() {
				t.Fatal("unexpected database configuration error")
			}
		})
	}
}

func TestProfileRelationsAndRuntimeReadiness(t *testing.T) {
	profileValue, err := (runtimeprofile.Profile{
		DatabaseName:          "account",
		RuntimeDatabaseRole:   "account_runtime",
		MigrationDatabaseRole: "account_migrator",
		DatabaseOwner:         "postgres",
		SchemaName:            "account",
		SchemaOwner:           "account_migrator",
	}).ForOperation(runtimeprofile.OperationRuntime)
	if err != nil {
		t.Fatal(err)
	}
	profile := &profileValue
	if got := profileRelation(profile, "accounts"); got != `"account"."accounts"` {
		t.Fatalf("account relation = %q", got)
	}
	if got := profileRelation(nil, "accounts"); got != "accounts" {
		t.Fatalf("legacy relation = %q", got)
	}
	if got := profileHistoryTable(profile); got != "account.schema_migrations" {
		t.Fatalf("account history = %q", got)
	}
	if err := validateRuntimeIdentity(profile, "account", "account_runtime", "postgres", "account_migrator"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct {
		role        string
		schemaOwner string
	}{
		{"account_migrator", "account_migrator"},
		{"account_runtime", "other"},
	} {
		if err := validateRuntimeIdentity(profile, "account", state.role, "postgres", state.schemaOwner); err == nil {
			t.Fatalf("invalid readiness accepted: %+v", state)
		}
	}
	migrationProfile, err := profileValue.ForOperation(runtimeprofile.OperationMigrate)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeIdentity(&migrationProfile, "account", "account_migrator", "postgres", "account_migrator"); err == nil {
		t.Fatal("migration operation accepted by runtime readiness")
	}
}
