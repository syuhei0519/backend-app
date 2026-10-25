package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/syuhei0519/backend-app/internal/account"
)

type readinessFixture struct{ unavailable atomic.Bool }

func (*readinessFixture) List(context.Context) ([]account.Account, error) { return nil, nil }
func (*readinessFixture) Get(context.Context, int64) (account.Account, error) {
	return account.Account{}, account.ErrNotFound
}
func (*readinessFixture) Create(context.Context, account.Input) (account.Account, error) {
	return account.Account{}, nil
}
func (*readinessFixture) Update(context.Context, int64, account.Input) (account.Account, error) {
	return account.Account{}, nil
}
func (*readinessFixture) Delete(context.Context, int64) error { return nil }
func (f *readinessFixture) Ready(context.Context) error {
	if f.unavailable.Load() {
		return errors.New("private database secret rejected")
	}
	return nil
}

func TestTransientDatabaseOutageOnlyChangesReadiness(t *testing.T) {
	fixture := &readinessFixture{}
	server := New(fixture, fixture, slog.New(slog.NewTextHandler(&strings.Builder{}, nil)))
	request := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		return response
	}

	fixture.unavailable.Store(true)
	if response := request("/readyz"); response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("unavailable readiness response = %d %q", response.Code, response.Body.String())
	}
	if response := request("/livez"); response.Code != http.StatusOK {
		t.Fatalf("liveness during database outage = %d", response.Code)
	}

	fixture.unavailable.Store(false)
	if response := request("/readyz"); response.Code != http.StatusOK {
		t.Fatalf("readiness after database recovery = %d", response.Code)
	}
}
