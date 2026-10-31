package observability

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"
)

// Profiling uses a separate, opt-in listener, never the business HTTP mux.
// Profiles can contain application data: operators must keep this listener on
// a private interface and supply a dedicated bearer token, even on loopback.
func Profiling() (func(context.Context) error, error) {
	address := os.Getenv("PROFILE_ADDR")
	if address == "" {
		return func(context.Context) error { return nil }, nil
	}
	token := os.Getenv("PROFILE_TOKEN")
	if len(token) < 32 || strings.TrimSpace(token) != token {
		return nil, errors.New("PROFILE_TOKEN must contain 32+ bytes without surrounding whitespace")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, errors.New("profiling listener unavailable")
	}
	server := &http.Server{Handler: profileHandler(token), ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 10 * time.Second,
		MaxHeaderBytes: 8 << 10}
	go func() { _ = server.Serve(listener) }()
	return server.Shutdown, nil
}

func profileHandler(token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/profile", func(w http.ResponseWriter, r *http.Request) {
		seconds, err := strconv.Atoi(r.URL.Query().Get("seconds"))
		if err != nil || seconds < 1 || seconds > 30 {
			http.Error(w, "seconds must be between 1 and 30", http.StatusBadRequest)
			return
		}
		pprof.Profile(w, r)
	})
	for _, name := range []string{"heap", "allocs", "goroutine"} {
		mux.Handle("/debug/pprof/"+name, pprof.Handler(name))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Heap/allocs delta profiles also accept seconds. Bound those requests
		// so a diagnostic connection cannot live beyond the server deadline.
		if raw := r.URL.Query().Get("seconds"); raw != "" {
			seconds, err := strconv.Atoi(raw)
			if err != nil || seconds < 1 || seconds > 30 {
				http.Error(w, "invalid profile duration", http.StatusBadRequest)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
