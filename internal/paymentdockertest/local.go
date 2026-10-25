// Package paymentdockertest binds Payment integration tests to the existing
// PLAT-46 local Docker fixture. The legacy Kubernetes fixture remains unchanged
// and is not imported by the four-service CI suite.
package paymentdockertest

import (
	"testing"

	"github.com/syuhei0519/backend-app/internal/ordertest"
)

type Database = ordertest.Database

func Enabled(t *testing.T) {
	t.Helper()
	ordertest.Enabled(t)
}

func Start(t *testing.T) *Database {
	t.Helper()
	return ordertest.Start(t, "payments", "payment_app")
}
