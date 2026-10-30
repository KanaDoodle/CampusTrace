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
	if a.Store.Search != nil {
		pending, ready, e := a.Store.SearchSyncStats(ctx)
		if e == nil {
			v := 0.
			if ready {
				v = 1
			}
			gauges = append(gauges, observability.Gauge{Name: "search_pending", Value: float64(pending)}, observability.Gauge{Name: "search_ready", Value: v})
		}
	}
	if a.Store.JobEvents {
		var pending int64
		var age float64
		if e := a.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(TIMESTAMPDIFF(MICROSECOND,MIN(occurred_at),UTC_TIMESTAMP(6))/1000000,0) FROM job_event_outbox WHERE published_at IS NULL").Scan(&pending, &age); e == nil {
			gauges = append(gauges, observability.Gauge{Name: "job_event_outbox_pending", Value: float64(pending)}, observability.Gauge{Name: "job_event_publish_age_seconds", Value: age})
		}
	}
	a.Metrics.Prometheus(w, r, a.Store.DB.Stats(), gauges...)
}
