package observability

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"
)

var latencyBounds = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 180}

type histogram struct {
	Count   uint64
	Sum     float64
	Buckets []uint64
}
type Metrics struct {
	mu      sync.Mutex
	Values  map[string]float64
	hist    map[string]*histogram
	started time.Time
}

func New() *Metrics {
	return &Metrics{Values: map[string]float64{}, hist: map[string]*histogram{}, started: time.Now()}
}
func (m *Metrics) Add(k string, v float64) { m.mu.Lock(); defer m.mu.Unlock(); m.Values[k] += v }
func (m *Metrics) Observe(k string, v float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hist == nil {
		m.hist = map[string]*histogram{}
	}
	h := m.hist[k]
	if h == nil {
		h = &histogram{Buckets: make([]uint64, len(latencyBounds))}
		m.hist[k] = h
	}
	h.Count++
	h.Sum += v
	for i, b := range latencyBounds {
		if v <= b {
			h.Buckets[i]++
		}
	}
}
func (m *Metrics) Since(k string, start time.Time) {
	v := time.Since(start).Seconds()
	m.Add(k+"_seconds", v)
	m.Add(k+"_count", 1)
	m.Observe(k+"_duration_seconds", v)
}
func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m.Values)
}
func (m *Metrics) Prometheus(w http.ResponseWriter, r *http.Request, db sql.DBStats) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	keys := []string{}
	for k := range m.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "campustrace_%s %g\n", k, m.Values[k])
	}
	keys = keys[:0]
	for k := range m.hist {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		h := m.hist[k]
		fmt.Fprintf(w, "# TYPE campustrace_%s histogram\n", k)
		for i, b := range latencyBounds {
			fmt.Fprintf(w, "campustrace_%s_bucket{le=%q} %d\n", k, strconv.FormatFloat(b, 'g', -1, 64), h.Buckets[i])
		}
		fmt.Fprintf(w, "campustrace_%s_bucket{le=\"+Inf\"} %d\ncampustrace_%s_sum %g\ncampustrace_%s_count %d\n", k, h.Count, k, h.Sum, k, h.Count)
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	for _, g := range []struct {
		name string
		v    float64
	}{{"go_goroutines", float64(runtime.NumGoroutine())}, {"go_heap_bytes", float64(mem.HeapAlloc)}, {"process_uptime_seconds", time.Since(m.started).Seconds()}, {"db_connections_open", float64(db.OpenConnections)}, {"db_connections_in_use", float64(db.InUse)}, {"db_connections_idle", float64(db.Idle)}, {"db_wait_total", float64(db.WaitCount)}, {"db_wait_seconds_total", db.WaitDuration.Seconds()}} {
		fmt.Fprintf(w, "campustrace_%s %g\n", g.name, g.v)
	}
}
