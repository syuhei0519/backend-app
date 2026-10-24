// Static scanner fixture only. No main, init, test or application import.
// Activate only on a disposable PR branch and remove before any merge.
package sastverification

import (
	"database/sql"
	"net/http"
)

func codeqlOnly(db *sql.DB, r *http.Request) {
	db.QueryContext(r.Context(), "SELECT "+r.URL.Query().Get("q"))
}
