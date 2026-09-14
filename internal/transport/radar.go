package transport

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"net/http"
	"strconv"
)

func (a *API) radarRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/sources", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.SourcesForUser(r.Context(), user(r))
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
