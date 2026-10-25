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
