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
	ExistingKindAccount    = "existing-kind-account"
	LearningAccountRelease = "learning-account-release"
)

var errInvalid = errors.New("invalid Account runtime profile")

// Profile binds a process to one reviewed database and telemetry identity.
// Passwords and other secret values are intentionally not represented here.
type Profile struct {
	Name              string
	DatabaseHost      string
	DatabaseName      string
	DatabaseRole      string
	DatabaseOwner     string
	Namespace         string
	CollectorEndpoint string
	Environment       string
}

var profiles = map[string]Profile{
	ExistingKindAccount: {
		Name:              ExistingKindAccount,
		DatabaseHost:      "postgresql",
		DatabaseName:      "account",
		DatabaseRole:      "account_app",
		DatabaseOwner:     "account_app",
		Namespace:         "account",
		CollectorEndpoint: "http://otel-collector.observability.svc.cluster.local:4318",
		Environment:       "local",
	},
	LearningAccountRelease: {
		Name:              LearningAccountRelease,
		DatabaseHost:      "learning-account-db",
		DatabaseName:      "account",
		DatabaseRole:      "account_app",
		DatabaseOwner:     "account_app",
		Namespace:         "learning-account-release",
		CollectorEndpoint: "http://account-release-otel-collector.observability.svc.cluster.local:4318",
		Environment:       "local",
	},
}

// Load validates only explicit profile mode. Empty keeps the compatible image
// phase working until both manifests supply profile and Downward API inputs.
func Load() (*Profile, error) {
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
	expected := map[string]string{
		"PGHOST":                      profile.DatabaseHost,
		"PGPORT":                      "5432",
		"PGDATABASE":                  profile.DatabaseName,
		"PGUSER":                      profile.DatabaseRole,
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
	for _, key := range []string{
		"PGSERVICE", "PGSERVICEFILE", "PGPASSFILE", "PGOPTIONS", "PGAPPNAME",
		"PGTARGETSESSIONATTRS", "PGCHANNELBINDING", "PGREQUIRESSL", "PGCONNECT_TIMEOUT",
		"PGKRBSRVNAME", "PGGSSLIB", "PGSSLROOTCERT", "PGSSLCERT", "PGSSLKEY", "PGSSLCRL",
	} {
		if os.Getenv(key) != "" {
			return nil, errInvalid
		}
	}
	return &profile, nil
}

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
func (p Profile) ValidateDatabaseIdentity(database, role, owner string) error {
	if database != p.DatabaseName || role != p.DatabaseRole || owner != p.DatabaseOwner {
		return errors.New("Account database identity rejected")
	}
	return nil
}
