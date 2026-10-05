package transport

import (
	"context"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

func (a *API) campusPreview(w http.ResponseWriter, r *http.Request, raw string) (source.CampusPreview, bool) {
	if source.RecognizeCampusURL(raw) != nil {
		codedError(w, http.StatusBadRequest, "SOURCE_URL_UNSUPPORTED")
		return source.CampusPreview{}, false
	}
	ok, err := a.Queue.Allow(r.Context(), "source-preview:"+user(r), 5, time.Minute)
	if err != nil {
		write(w, nil, p.ErrBackendUnavailable)
		return source.CampusPreview{}, false
	}
	if !ok {
		codedError(w, http.StatusTooManyRequests, "SOURCE_PREVIEW_RATE_LIMIT")
		return source.CampusPreview{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	v, err := (source.PublicPlatform{Allow: func(ctx context.Context, key string, limit int) (bool, error) {
		return a.Queue.Allow(ctx, "source:"+key, limit, time.Minute)
	}}).PreviewCampus(ctx, raw)
	if err != nil {
		code := sourcePreviewFailure(err)
		var fetch *source.FetchError
		if source.AsFetchError(err, &fetch) {
			slog.WarnContext(r.Context(), "campus source preview failed", "category", fetch.Category, "upstream_status", fetch.HTTPStatus)
		}
		codedError(w, http.StatusBadGateway, code)
		return source.CampusPreview{}, false
	}
	return v, true
}

func sourcePreviewFailure(err error) string {
	var fetch *source.FetchError
	if source.AsFetchError(err, &fetch) {
		switch fetch.Category {
		case "TIMEOUT_OR_NETWORK":
			return "SOURCE_PREVIEW_NETWORK"
		case "BLOCKED":
			return "SOURCE_PREVIEW_BLOCKED"
		case "RATE_LIMIT":
			return "SOURCE_PREVIEW_BUSY"
		case "HTTP_TRANSIENT":
			return "SOURCE_PREVIEW_BUSY"
		case "SCHEMA_INVALID", "RESPONSE_TOO_LARGE":
			return "SOURCE_PREVIEW_CHANGED"
		case "CAPACITY":
			return "SOURCE_PREVIEW_CAPACITY"
		}
	}
	return "SOURCE_PREVIEW_FAILED"
}

func (a *API) radarRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/sources/catalog", func(w http.ResponseWriter, r *http.Request) { write(w, source.CampusSites(), nil) })
	on("GET /api/radar/todos", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Todos(r.Context(), user(r))
		w.Header().Set("Cache-Control", "no-store")
		if errors.Is(err, p.ErrTodoCapacity) {
			codedError(w, http.StatusConflict, "TODO_CAPACITY")
			return
		}
		write(w, v, err)
	})
	on("GET /api/sources", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.SourcesForUser(r.Context(), user(r))
		write(w, v, err)
	})
	on("POST /api/sources/preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		v, ok := a.campusPreview(w, r, in.URL)
		if ok {
			write(w, v, nil)
		}
	})
	on("POST /api/sources/from-url", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL           string `json:"url"`
			CheckInterval int    `json:"check_interval"`
			Keyword       string `json:"keyword"`
			Direction     string `json:"direction"`
			Enabled       bool   `json:"enabled"`
			Adaptive      bool   `json:"adaptive,omitempty"`
			Priority      bool   `json:"priority,omitempty"`
		}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		if err := (d.WatchInput{SourceID: "pending", CheckInterval: in.CheckInterval, Keyword: in.Keyword, Direction: in.Direction, Enabled: in.Enabled}).Validate(); err != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		v, ok := a.campusPreview(w, r, in.URL)
		if !ok {
			return
		}
		if (!v.SupportsDirection && in.Direction != "") || in.CheckInterval < v.MinimumInterval {
			write(w, nil, p.ErrValidation)
			return
		}
		result, err := a.Store.CreateCampusSource(r.Context(), user(r), v.Adapter, v.ProjectCode, v.Name, d.WatchInput{CheckInterval: in.CheckInterval, Keyword: in.Keyword, Direction: in.Direction, Enabled: in.Enabled, Adaptive: in.Adaptive, Priority: in.Priority})
		write(w, result, err)
	})
	on("GET /api/sources/{id}/jobs", func(w http.ResponseWriter, r *http.Request) {
		page := 1
		var err error
		if raw := r.URL.Query().Get("page"); raw != "" {
			page, err = strconv.Atoi(raw)
		}
		if err != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		v, err := a.Store.SourceJobsForUser(r.Context(), user(r), r.PathValue("id"), page)
		write(w, v, err)
	})
	on("GET /api/watches", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Watches(r.Context(), user(r))
		write(w, v, err)
	})
	on("GET /api/watches/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Watch(r.Context(), user(r), r.PathValue("id"))
		write(w, v, err)
	})
	on("GET /api/watches/{id}/progress", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.ProgressForUser(r.Context(), user(r), r.PathValue("id"))
		write(w, v, err)
	})
	on("POST /api/watches", func(w http.ResponseWriter, r *http.Request) {
		var in d.WatchInput
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.CreateWatch(r.Context(), user(r), in)
		write(w, v, err)
	})
	on("PUT /api/watches/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in d.WatchInput
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.UpdateWatch(r.Context(), user(r), r.PathValue("id"), in)
		write(w, v, err)
	})
	on("DELETE /api/watches/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := a.Store.DeleteWatch(r.Context(), user(r), r.PathValue("id"))
		write(w, map[string]bool{"deleted": err == nil}, err)
	})
	on("GET /api/radar/digest", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.DailyDigest(r.Context(), user(r))
		write(w, v, err)
	})
	on("GET /api/radar/changes", func(w http.ResponseWriter, r *http.Request) {
		days := 1
		var err error
		if raw := r.URL.Query().Get("days"); raw != "" {
			days, err = strconv.Atoi(raw)
		}
		if err != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		v, err := a.Store.RecentChanges(r.Context(), user(r), days)
		write(w, v, err)
	})
	on("GET /api/radar/closing", func(w http.ResponseWriter, r *http.Request) {
		days := 7
		var err error
		if raw := r.URL.Query().Get("days"); raw != "" {
			days, err = strconv.Atoi(raw)
		}
		if err != nil {
			write(w, nil, p.ErrValidation)
			return
		}
		v, err := a.Store.ClosingJobs(r.Context(), user(r), days)
		write(w, v, err)
	})
	on("GET /api/preferences", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Preferences(r.Context(), user(r))
		write(w, v, err)
	})
	on("PUT /api/jobs/preferences", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			JobIDs      []string `json:"job_ids"`
			Disposition string   `json:"disposition"`
		}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.SetBulkPreference(r.Context(), user(r), in.JobIDs, in.Disposition)
		write(w, v, err)
	})
	on("PUT /api/jobs/{id}/preference", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Disposition string `json:"disposition"`
		}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		err := a.Store.SetPreference(r.Context(), user(r), r.PathValue("id"), in.Disposition)
		write(w, map[string]string{"disposition": in.Disposition}, err)
	})
	on("GET /api/notifications", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Notifications(r.Context(), user(r))
		write(w, v, err)
	})
	on("POST /api/notifications/refresh", func(w http.ResponseWriter, r *http.Request) {
		created, dedup, err := a.Store.RefreshNotifications(r.Context(), user(r))
		write(w, map[string]int{"created": created, "deduplicated": dedup}, err)
	})
	on("POST /api/notifications/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		err := a.Store.ReadNotification(r.Context(), user(r), r.PathValue("id"))
		write(w, map[string]bool{"read": err == nil}, err)
	})
}
