package query

import "net/http"

// mountExport는 /export/samples, /export/profile, /export/logs를 붙인다. (stub: C5가 채움)
func mountExport(mux *http.ServeMux, d Deps) {}
