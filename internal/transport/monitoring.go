package transport

import (
	"context"
	"net/http"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/searchapi"
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
	active, limit, fallback, fallbackLimit := a.Store.SearchReadStats()
	gauges = append(gauges, observability.Gauge{Name: "search_read_active", Value: float64(active)}, observability.Gauge{Name: "search_read_limit", Value: float64(limit)}, observability.Gauge{Name: "search_fallback_active", Value: float64(fallback)}, observability.Gauge{Name: "search_fallback_limit", Value: float64(fallbackLimit)})
	if query, ok := a.Store.Search.(interface{ QueryState() searchapi.QueryState }); ok {
		state := query.QueryState()
		code := 0.
		if state.State == "open" {
			code = 1
		}
		if state.State == "half_open" {
			code = 2
		}
		gauges = append(gauges, observability.Gauge{Name: "search_query_circuit_state", Value: code}, observability.Gauge{Name: "search_query_active", Value: float64(state.Active)}, observability.Gauge{Name: "search_query_limit", Value: float64(state.Limit)}, observability.Gauge{Name: "search_query_retry_seconds", Value: state.RetryAfter.Seconds()})
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

	if a.Store.Search != nil {
		phase, copied, total, pending, e := a.Store.SearchRebuildStats(ctx)
		if e == nil {
			gauges = append(gauges, observability.Gauge{Name: "search_rebuild_phase", Value: float64(phase)}, observability.Gauge{Name: "search_rebuild_copied", Value: float64(copied)}, observability.Gauge{Name: "search_rebuild_documents", Value: float64(total)}, observability.Gauge{Name: "search_rebuild_pending", Value: float64(pending)})
		}
	}
	if a.Store.JobEvents {
		var pending int64
		var age float64
		if e := a.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(TIMESTAMPDIFF(MICROSECOND,MIN(occurred_at),UTC_TIMESTAMP(6))/1000000,0) FROM job_event_outbox WHERE dataset=(SELECT dataset FROM search_revision_state WHERE id='jobs') AND published_at IS NULL").Scan(&pending, &age); e == nil {
			gauges = append(gauges, observability.Gauge{Name: "job_event_outbox_pending", Value: float64(pending)}, observability.Gauge{Name: "job_event_publish_age_seconds", Value: age})
		}
	}
	a.Metrics.Prometheus(w, r, a.Store.DB.Stats(), gauges...)
}
