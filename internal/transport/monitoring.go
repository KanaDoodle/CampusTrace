package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/observability"
)

func (a *API) prometheus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	gauges := []observability.Gauge{}
	if a.ModelControl != nil {
		stats, e := a.Store.ModelPoolMetrics(ctx, a.ModelControl.Options.Policy.ID)
		up := 0.
		if e == nil {
			up = 1
			for name, value := range map[string]int{"model_pool_waiting": stats.Waiting, "model_pool_active": stats.Active, "model_pool_quarantined": stats.Quarantined, "model_pool_unknown": stats.Unknown, "model_pool_daily_calls": stats.AllocatedToday, "model_pool_capacity": stats.Policy.Capacity, "model_pool_daily_limit": stats.Policy.DailyLimit} {
				gauges = append(gauges, observability.Gauge{Name: name, Value: float64(value)})
			}
		}
		gauges = append(gauges, observability.Gauge{Name: "model_pool_scrape_success", Value: up})
	}
	a.Metrics.Prometheus(w, r, a.Store.DB.Stats(), gauges...)
}
