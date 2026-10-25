package runtimeprofile

import (
	"os"
	"strings"
	"testing"
)

func clearProfileEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "DATABASE_URL" || key == "ACCOUNT_RUNTIME_PROFILE" || strings.HasPrefix(key, "ACCOUNT_RUNTIME_PROFILE_") || strings.HasPrefix(key, "PG") || strings.HasPrefix(key, "OTEL_EXPORTER_OTLP_") || key == "POD_NAMESPACE" || key == "POD_UID" || key == "DEPLOYMENT_ENVIRONMENT" {
			t.Setenv(key, "")
		}
	}
}

func setProfile(t *testing.T, profile Profile, operation Operation) Profile {
	t.Helper()
	profile, err := profile.ForOperation(operation)
	if err != nil {
		t.Fatal(err)
	}
	role, err := profile.DatabaseRole()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACCOUNT_RUNTIME_PROFILE", profile.Name)
	t.Setenv("PGHOST", profile.DatabaseHost)
	t.Setenv("PGPORT", "5432")
	t.Setenv("PGDATABASE", profile.DatabaseName)
	t.Setenv("PGUSER", role)
	t.Setenv("PGPASSWORD", "fixture-secret")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("POD_NAMESPACE", profile.Namespace)
	t.Setenv("POD_UID", "fixture-pod-uid")
	t.Setenv("DEPLOYMENT_ENVIRONMENT", profile.Environment)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", profile.CollectorEndpoint)
	return profile
}

func TestFiniteProfilesLoad(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation Operation
		role      string
	}{
		{CurrentExistingKindAccount, OperationRuntime, "account_app"},
		{CurrentExistingKindAccount, OperationMigrate, "account_app"},
		{FreshAccount, OperationRuntime, "account_runtime"},
		{FreshAccount, OperationMigrate, "account_migrator"},
	} {
		t.Run(test.name+"-"+string(test.operation), func(t *testing.T) {
			clearProfileEnvironment(t)
			setProfile(t, profiles[test.name], test.operation)
			got, err := Load(test.operation)
			if err != nil || got == nil || got.Name != test.name {
				t.Fatalf("profile=%v err=%v", got, err)
			}
			role, err := got.DatabaseRole()
			if err != nil || role != test.role {
				t.Fatalf("role=%q err=%v", role, err)
			}
		})
	}
}

func TestLegacyCompatibleWhenProfileAbsent(t *testing.T) {
	clearProfileEnvironment(t)
	got, err := Load(OperationRuntime)
	if err != nil || got != nil {
		t.Fatalf("profile=%v err=%v", got, err)
	}
}

func TestUnknownProfileSettingNeverFallsBackToLegacy(t *testing.T) {
	clearProfileEnvironment(t)
	t.Setenv("ACCOUNT_RUNTIME_PROFILE_TARGET", "other")
	if _, err := Load(OperationRuntime); err == nil {
		t.Fatal("unknown profile setting fell back to legacy mode")
	}
}

func TestProfileInputsFailClosedWithoutLeakingValues(t *testing.T) {
	secret := "private-fixture-secret"
	tests := []struct {
		name string
		edit func(*testing.T)
	}{
		{"unknown-profile", func(t *testing.T) { t.Setenv("ACCOUNT_RUNTIME_PROFILE", "unknown") }},
		{"wrong-database", func(t *testing.T) { t.Setenv("PGDATABASE", "other") }},
		{"wrong-role", func(t *testing.T) { t.Setenv("PGUSER", "postgres") }},
		{"missing-secret", func(t *testing.T) { t.Setenv("PGPASSWORD", "") }},
		{"wrong-namespace", func(t *testing.T) { t.Setenv("POD_NAMESPACE", "other") }},
		{"wrong-collector", func(t *testing.T) { t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector.invalid:4318") }},
		{"database-url-override", func(t *testing.T) { t.Setenv("DATABASE_URL", "postgres://user:"+secret+"@db/other") }},
		{"libpq-override", func(t *testing.T) { t.Setenv("PGSERVICE", secret) }},
		{"unknown-profile-setting", func(t *testing.T) { t.Setenv("ACCOUNT_RUNTIME_PROFILE_TARGET", secret) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearProfileEnvironment(t)
			setProfile(t, profiles[CurrentExistingKindAccount], OperationRuntime)
			t.Setenv("PGPASSWORD", secret)
			test.edit(t)
			_, err := Load(OperationRuntime)
			if err == nil {
				t.Fatal("invalid profile accepted")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "collector.invalid") {
				t.Fatal("profile input leaked")
			}
		})
	}
}

func TestAllUnreviewedDriverSettingsFailClosed(t *testing.T) {
	secret := "private-driver-canary"
	for _, key := range []string{
		"PGPASSFILE", "PGAPPNAME", "PGCONNECT_TIMEOUT", "PGSSLCERT", "PGSSLKEY",
		"PGSSLSNI", "PGSSLROOTCERT", "PGSSLPASSWORD", "PGSSLNEGOTIATION",
		"PGTARGETSESSIONATTRS", "PGSERVICE", "PGSERVICEFILE", "PGTZ", "PGOPTIONS",
		"PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION", "PGCHANNELBINDING", "PGREQUIREAUTH",
		"PG_FUTURE_DRIVER_SETTING",
	} {
		t.Run(key, func(t *testing.T) {
			clearProfileEnvironment(t)
			setProfile(t, profiles[CurrentExistingKindAccount], OperationRuntime)
			t.Setenv(key, secret)
			_, err := Load(OperationRuntime)
			if err == nil {
				t.Fatal("unreviewed driver setting accepted")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatal("driver setting value leaked")
			}
		})
	}
}

func TestDatabaseIdentityUsesActualValues(t *testing.T) {
	profile, err := profiles[FreshAccount].ForOperation(OperationRuntime)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.ValidateDatabaseIdentity("account", "account_runtime", "postgres", "account_migrator"); err != nil {
		t.Fatal(err)
	}
	for _, values := range [][4]string{
		{"other", "account_runtime", "postgres", "account_migrator"},
		{"account", "account_migrator", "postgres", "account_migrator"},
		{"account", "account_runtime", "other", "account_migrator"},
		{"account", "account_runtime", "postgres", "other"},
	} {
		if err := profile.ValidateDatabaseIdentity(values[0], values[1], values[2], values[3]); err == nil {
			t.Fatal("wrong actual database identity accepted")
		}
	}
}

func TestOldFreshProfileNameIsNotAnAlias(t *testing.T) {
	clearProfileEnvironment(t)
	t.Setenv("ACCOUNT_RUNTIME_PROFILE", "fresh-learning-account-release")
	if _, err := Load(OperationRuntime); err == nil {
		t.Fatal("retired fresh profile alias accepted")
	}
}
