package analysis

import (
	"net/http"
)

func (r *RunningServer) HealthHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if r.root.Err() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !r.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("registration degraded\n"))
			return
		}
		w.Write([]byte("ready\n"))
	})
	return mux
}
