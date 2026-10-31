package observability

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProfilingDisabledAndInvalidConfiguration(t *testing.T) {
	t.Setenv("PROFILE_ADDR", "")
	close, err := Profiling()
	if err != nil || close(context.Background()) != nil {
		t.Fatal(err)
	}
	t.Setenv("PROFILE_ADDR", "127.0.0.1:0")
	t.Setenv("PROFILE_TOKEN", "short")
	if _, err = Profiling(); err == nil {
		t.Fatal("missing token accepted")
	}
	t.Setenv("PROFILE_TOKEN", strings.Repeat("x", 32))
	close, err = Profiling()
	if err != nil {
		t.Fatal(err)
	}
	if err = close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProfileHandlerAdmission(t *testing.T) {
	token := strings.Repeat("x", 32)
	h := profileHandler(token)
	for _, tc := range []struct {
		path, method, token string
		status              int
	}{
		{"/debug/pprof/heap", "GET", "", 401},
		{"/debug/pprof/heap", "GET", token + "x", 401},
		{"/debug/pprof/heap", "POST", token, 405},
		{"/debug/pprof/profile", "GET", token, 400},
		{"/debug/pprof/profile?seconds=31", "GET", token, 400},
		{"/debug/pprof/allocs?seconds=100", "GET", token, 400},
		{"/debug/pprof/cmdline", "GET", token, 404},
		{"/debug/pprof/heap", "GET", token, 200},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: got %d want %d", tc.path, w.Code, tc.status)
		}
	}
}
