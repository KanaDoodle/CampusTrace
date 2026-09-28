package transport

import (
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"net/http"
)

func (a *API) campaignRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/application-campaigns", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.Campaigns(r.Context(), user(r))
		w.Header().Set("Cache-Control", "no-store")
		write(w, v, err)
	})
	on("GET /api/application-campaigns/catalog", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Store.CampaignCatalog(r.Context(), user(r))
		write(w, v, err)
	})
	for _, route := range []string{"POST /api/application-campaigns", "PUT /api/application-campaigns/{id}"} {
		on(route, func(w http.ResponseWriter, r *http.Request) {
			var v p.ApplicationCampaign
			if err := decode(r, &v); err != nil {
				write(w, nil, err)
				return
			}
			if r.Method == "PUT" && len(r.PathValue("id")) != 32 {
				write(w, nil, p.ErrValidation)
				return
			}
			out, err := a.Store.SaveCampaign(r.Context(), user(r), r.PathValue("id"), v)
			write(w, out, err)
		})
	}
	on("DELETE /api/application-campaigns/{id}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Version int `json:"version"`
		}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		err := a.Store.DeleteCampaign(r.Context(), user(r), r.PathValue("id"), in.Version)
		write(w, map[string]bool{"deleted": err == nil}, err)
	})
}
