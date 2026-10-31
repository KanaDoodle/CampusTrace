package transport

import "net/http"

// Retain authenticated history access without keeping an execution service.
func (a *API) practiceRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/practice/capabilities", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		write(w, map[string]any{"enabled": false, "retired": true, "history_available": true}, nil)
	})
	on("GET /api/practice/runs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, e := a.Store.PracticeRuns(r.Context(), user(r))
		write(w, v, e)
	})
	on("POST /api/practice/runs", func(w http.ResponseWriter, r *http.Request) {
		codedError(w, http.StatusGone, "PRACTICE_RETIRED")
	})
}
