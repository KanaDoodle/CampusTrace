package transport

import (
	"errors"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"net/http"
)

func (a *API) sourceImportRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/sources/imports/latest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		v, err := a.Store.LatestSourceImport(r.Context(), user(r))
		write(w, v, err)
	})
	on("POST /api/sources/import-all", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		specs := []p.SourceImportSpec{}
		for _, s := range source.CampusSites() {
			specs = append(specs, p.SourceImportSpec{Adapter: s.Adapter, Company: s.Company, URL: s.URL, Scope: s.Scope})
		}
		v, err := a.Store.StartSourceImport(r.Context(), user(r), specs)
		if errors.Is(err, p.ErrSourceImportCooldown) {
			codedError(w, http.StatusConflict, "SOURCE_IMPORT_COOLDOWN")
			return
		}
		write(w, v, err)
	})
	on("POST /api/sources/imports/{id}/retry", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if err := decode(r, &in); err != nil {
			write(w, nil, err)
			return
		}
		v, err := a.Store.RetrySourceImport(r.Context(), user(r), r.PathValue("id"))
		write(w, v, err)
	})
}
