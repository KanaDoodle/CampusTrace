package observability

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type Metrics struct {
	mu     sync.Mutex
	Values map[string]float64
}

func New() *Metrics                        { return &Metrics{Values: map[string]float64{}} }
func (m *Metrics) Add(k string, v float64) { m.mu.Lock(); defer m.mu.Unlock(); m.Values[k] += v }
func (m *Metrics) Since(k string, start time.Time) {
	m.Add(k+"_seconds", time.Since(start).Seconds())
	m.Add(k+"_count", 1)
}
func (m *Metrics) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m.Values)
}
