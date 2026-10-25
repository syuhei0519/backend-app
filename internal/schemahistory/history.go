// Package schemahistory gives every service the same finite interpretation of
// its migration history. It deliberately models the prepared version 2 without
// shipping or applying a version 2 migration.
package schemahistory

import (
	"context"
	"errors"
	"os"
	"sort"

	"github.com/jackc/pgx/v5"
)

type Policy string

const (
	StrictC0     Policy = ""
	PreparedV1V2 Policy = "prepared-v1-v2"
)

var ErrInvalidPolicy = errors.New("invalid schema compatibility profile")

// LoadPolicy exposes one finite rollback window. Empty remains the production
// C0 contract; the prepared profile may read/write the unchanged C0 columns
// while schema histories {1} and {1,2} have both been tested.
func LoadPolicy() (Policy, error) {
	policy := Policy(os.Getenv("SCHEMA_COMPATIBILITY_PROFILE"))
	if policy != StrictC0 && policy != PreparedV1V2 {
		return StrictC0, ErrInvalidPolicy
	}
	return policy, nil
}

func (p Policy) Ready(state State) bool {
	if p == PreparedV1V2 {
		return state == C0 || state == Expand
	}
	return state == C0
}

type State string

const (
	Unavailable State = "unavailable"
	NoTable     State = "no-table"
	Empty       State = "empty"
	C0          State = "c0-v1"
	Expand      State = "expand-v1-v2"
	MissingBase State = "missing-base-v2"
	Gap         State = "gap"
	Future      State = "unknown-future"
)

// Classify is pure so the complete state matrix is testable without a DB. The
// available argument represents a successful DB/history read, not readiness.
func Classify(available, tableExists bool, versions []int) State {
	if !available {
		return Unavailable
	}
	if !tableExists {
		return NoTable
	}
	if len(versions) == 0 {
		return Empty
	}
	values := append([]int(nil), versions...)
	sort.Ints(values)
	if len(values) == 1 && values[0] == 1 {
		return C0
	}
	if len(values) == 2 && values[0] == 1 && values[1] == 2 {
		return Expand
	}
	if len(values) == 1 && values[0] == 2 {
		return MissingBase
	}
	for i, version := range values {
		if version != i+1 {
			return Gap
		}
	}
	if values[len(values)-1] > 2 {
		return Future
	}
	return Gap
}

// C0Ready is intentionally exact. A minimum-version comparison would let an
// old binary run against a schema whose compatibility has not been proven.
func C0Ready(state State) bool { return state == C0 }

// C0MigrationAllowed permits only a new empty history or an exact idempotent
// C0 history. Expand/future/gapped histories must be handled by a newer,
// explicitly tested migration binary.
func C0MigrationAllowed(state State) bool { return state == Empty || state == C0 }

type Queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

var historyQueries = map[string]string{
	"public.schema_migrations":              `SELECT version FROM public.schema_migrations ORDER BY version`,
	"account.schema_migrations":             `SELECT version FROM account.schema_migrations ORDER BY version`,
	"public.order_schema_migrations":        `SELECT version FROM public.order_schema_migrations ORDER BY version`,
	"public.payment_schema_migrations":      `SELECT version FROM public.payment_schema_migrations ORDER BY version`,
	"public.notification_schema_migrations": `SELECT version FROM public.notification_schema_migrations ORDER BY version`,
}

// Inspect reads only one of the fixed migration-history tables. The
// allow-list prevents a caller from turning a table name into SQL.
func Inspect(ctx context.Context, q Queryer, table string) State {
	query, ok := historyQueries[table]
	if !ok {
		return Unavailable
	}
	var resolved *string
	if err := q.QueryRow(ctx, `SELECT to_regclass($1)::text`, table).Scan(&resolved); err != nil {
		return Unavailable
	}
	if resolved == nil {
		return NoTable
	}
	rows, err := q.Query(ctx, query)
	if err != nil {
		return Unavailable
	}
	defer rows.Close()
	versions := []int{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return Unavailable
		}
		versions = append(versions, version)
	}
	if rows.Err() != nil {
		return Unavailable
	}
	return Classify(true, true, versions)
}
