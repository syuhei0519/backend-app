// Package runtimeprofile defines the finite Account runtime identities accepted
// by one backend image. An absent profile keeps the pre-PLAT-211 image contract
// during the staged rollout; any explicit profile is validated fail closed.
package runtimeprofile

import (
	"errors"
	"os"
	"strings"
)

const (
	CurrentExistingKindAccount = "current-existing-kind-account"
	FreshAccount               = "fresh-account"
)

// Operation binds an explicit profile to one of the two executable modes.
// The environment cannot select an arbitrary database role for either mode.
type Operation string

const (
	OperationRuntime Operation = "runtime"
	OperationMigrate Operation = "migrate"
)

var errInvalid = errors.New("invalid Account runtime profile")

// Profile binds a process to one reviewed database and telemetry identity.
// Passwords and other secret values are intentionally not represented here.
type Profile struct {
	Name                  string
	DatabaseHost          string
	DatabaseName          string
	RuntimeDatabaseRole   string
	MigrationDatabaseRole string
	DatabaseOwner         string
	SchemaName            string
	SchemaOwner           string
	Namespace             string
	CollectorEndpoint     string
	Environment           string
	operation             Operation
}

var profiles = map[string]Profile{
	CurrentExistingKindAccount: {
		Name:                  CurrentExistingKindAccount,
		DatabaseHost:          "learning-account-db",
		DatabaseName:          "account",
		RuntimeDatabaseRole:   "account_app",
		MigrationDatabaseRole: "account_app",
		DatabaseOwner:         "account_app",
		SchemaName:            "public",
		Namespace:             "learning-services-20261018",
		CollectorEndpoint:     "http://otel-collector.observability.svc.cluster.local:4318",
		Environment:           "local",
	},
	FreshAccount: {
		Name:                  FreshAccount,
		DatabaseHost:          "account-postgresql.fresh-account.svc",
		DatabaseName:          "account",
		RuntimeDatabaseRole:   "account_runtime",
		MigrationDatabaseRole: "account_migrator",
		DatabaseOwner:         "postgres",
		SchemaName:            "account",
		SchemaOwner:           "account_migrator",
		Namespace:             "fresh-account",
		CollectorEndpoint:     "http://otel-collector.fresh-observability.svc.cluster.local:4318",
		Environment:           "local",
	},
}

// Load validates only explicit profile mode. Empty keeps the compatible image
// phase working until both manifests supply profile and Downward API inputs.
func Load(operation Operation) (*Profile, error) {
	if operation != OperationRuntime && operation != OperationMigrate {
		return nil, errInvalid
	}
	name := os.Getenv("ACCOUNT_RUNTIME_PROFILE")
	if hasUnknownProfileSetting() {
		return nil, errInvalid
	}
	if name == "" {
		return nil, nil
	}
	profile, ok := profiles[name]
	if !ok {
		return nil, errInvalid
	}
	profile, err := profile.ForOperation(operation)
	if err != nil {
		return nil, err
	}
	role, err := profile.DatabaseRole()
	if err != nil {
		return nil, err
	}
	expected := map[string]string{
		"PGHOST":                      profile.DatabaseHost,
		"PGPORT":                      "5432",
		"PGDATABASE":                  profile.DatabaseName,
		"PGUSER":                      role,
		"PGSSLMODE":                   "disable",
		"POD_NAMESPACE":               profile.Namespace,
		"DEPLOYMENT_ENVIRONMENT":      profile.Environment,
		"OTEL_EXPORTER_OTLP_ENDPOINT": profile.CollectorEndpoint,
	}
	for key, value := range expected {
		if os.Getenv(key) != value {
			return nil, errInvalid
		}
	}
	if os.Getenv("PGPASSWORD") == "" || os.Getenv("POD_UID") == "" || os.Getenv("DATABASE_URL") != "" {
		return nil, errInvalid
	}
	allowedDriverSettings := map[string]struct{}{
		"PGHOST": {}, "PGPORT": {}, "PGDATABASE": {},
		"PGUSER": {}, "PGPASSWORD": {}, "PGSSLMODE": {},
	}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "PG") && value != "" {
			if _, ok := allowedDriverSettings[key]; !ok {
				return nil, errInvalid
			}
		}
	}
	return &profile, nil
}

// ForOperation resolves an immutable finite profile for one executable mode.
func (p Profile) ForOperation(operation Operation) (Profile, error) {
	if operation != OperationRuntime && operation != OperationMigrate {
		return Profile{}, errInvalid
	}
	p.operation = operation
	if _, err := p.DatabaseRole(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// DatabaseRole returns the one finite role allowed for the selected operation.
func (p Profile) DatabaseRole() (string, error) {
	switch p.operation {
	case OperationRuntime:
		if p.RuntimeDatabaseRole != "" {
			return p.RuntimeDatabaseRole, nil
		}
	case OperationMigrate:
		if p.MigrationDatabaseRole != "" {
			return p.MigrationDatabaseRole, nil
		}
	}
	return "", errInvalid
}

// Operation reports the executable mode resolved before configuration loading.
func (p Profile) Operation() Operation { return p.operation }

func hasUnknownProfileSetting() bool {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "ACCOUNT_RUNTIME_PROFILE_") {
			return true
		}
	}
	return false
}

// ValidateDatabaseIdentity checks values returned by PostgreSQL, not merely
// process environment. Its error intentionally omits received values.
func (p Profile) ValidateDatabaseIdentity(database, role, owner, schemaOwner string) error {
	expectedRole, err := p.DatabaseRole()
	if err != nil || database != p.DatabaseName || role != expectedRole || owner != p.DatabaseOwner {
		return errors.New("Account database identity rejected")
	}
	if p.SchemaOwner != "" && schemaOwner != p.SchemaOwner {
		return errors.New("Account database identity rejected")
	}
	return nil
}
