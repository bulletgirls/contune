package query

import "net/http"

// mountQuery는 /series, /hosts, /panels, /profile, /catalog, /logs, /hosts/{id}/procs를 붙인다. (stub: K2·C4가 채움)
func mountQuery(mux *http.ServeMux, d Deps) {}
