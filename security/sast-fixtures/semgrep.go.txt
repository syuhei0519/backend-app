// Static scanner fixture only. No main, init, test or application import.
// Activate only on a disposable PR branch and remove before any merge.
package sastverification

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

func semgrepOnly(db *pgxpool.Pool, r *http.Request) {
	db.QueryRow(r.Context(), "SELECT "+r.URL.Query().Get("q"))
}
