// Package sastfixture contains deliberate static-analysis input only.
// Nothing imports these functions, registers a handler, or executes this source.
// Remove this package after comparing scanner results on the Draft PR.
package sastfixture

import (
	"database/sql"
	"net/http"
	"os/exec"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NeverCallCommand(r *http.Request) {
	input := r.URL.Query().Get("cmd")
	_, _ = exec.Command("sh", "-c", input).Output()
}

func NeverCallSQL(db *sql.DB, r *http.Request) {
	rows, err := db.QueryContext(r.Context(), "SELECT name FROM accounts WHERE name='"+r.URL.Query().Get("name")+"'")
	if err == nil {
		_ = rows.Close()
	}
}

func NeverCallPgx(db *pgxpool.Pool, r *http.Request) {
	var ignored string
	_ = db.QueryRow(r.Context(), "SELECT name FROM accounts WHERE name='"+r.URL.Query().Get("name")+"'").Scan(&ignored)
}
