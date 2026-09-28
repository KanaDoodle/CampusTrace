package observability

import (
	"database/sql"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentHistogramsAndLegacyJSON(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				m.Add("requests_total", 1)
				m.Observe("latency_seconds", .1)
			}
		}()
	}
	wg.Wait()
	r := httptest.NewRecorder()
	m.ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(r.Body.String(), `"requests_total":1000`) {
		t.Fatal(r.Body.String())
	}
	r = httptest.NewRecorder()
	m.Prometheus(r, httptest.NewRequest("GET", "/metrics/prometheus", nil), sql.DBStats{InUse: 2, OpenConnections: 3})
	for _, want := range []string{`campustrace_latency_seconds_bucket{le="0.05"} 0`, `campustrace_latency_seconds_bucket{le="0.1"} 1000`, `campustrace_latency_seconds_bucket{le="+Inf"} 1000`, `campustrace_latency_seconds_count 1000`, `campustrace_db_connections_in_use 2`} {
		if !strings.Contains(r.Body.String(), want) {
			t.Fatal("missing", want, r.Body.String())
		}
	}
}
