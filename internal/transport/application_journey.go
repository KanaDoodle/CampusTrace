package transport

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"io"
	"net/http"
)

func (a *API) journeyRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/applications/{id}/dossier", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.ApplicationDossier(r.Context(), user(r), r.PathValue("id"))
		decisionWriteError(w, v, e)
	})
	on("POST /api/applications/{id}/context-preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ResumeText string `json:"resume_text"`
		}
		if e := decodeJourney(r, &in); e != nil {
			write(w, nil, e)
			return
		}
		v, e := a.Store.ApplicationContextPreview(r.Context(), user(r), r.PathValue("id"), in.ResumeText)
		decisionWriteError(w, v, e)
	})
	on("GET /api/recruitment-notices", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.RecruitmentNotices(r.Context(), user(r))
		decisionWriteError(w, v, e)
	})
	on("POST /api/recruitment-notices", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Content string `json:"content"`
			Format  string `json:"format"`
		}
		if e := decodeJourney(r, &in); e != nil {
			write(w, nil, e)
			return
		}
		v, e := a.Store.ImportRecruitmentNotice(r.Context(), user(r), in.Content, in.Format)
		decisionWriteError(w, v, e)
	})
	on("GET /api/recruitment-notices/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.Store.RecruitmentNotice(r.Context(), user(r), r.PathValue("id"))
		decisionWriteError(w, v, e)
	})
	on("POST /api/recruitment-notices/{id}/confirm", func(w http.ResponseWriter, r *http.Request) {
		var in p.NoticeConfirmation
		if e := decode(r, &in); e != nil {
			write(w, nil, e)
			return
		}
		v, e := a.Store.ConfirmRecruitmentNotice(r.Context(), user(r), r.PathValue("id"), in)
		decisionWriteError(w, v, e)
	})
	on("POST /api/matching/company-plan", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			companyWorkspaceRequest
			Selected []string `json:"selected_job_ids"`
			Key      string   `json:"decision_key"`
		}
		if e := decode(r, &in); e != nil {
			write(w, nil, e)
			return
		}
		identity, e := a.matchIdentity(in.matchPreviewRequest)
		if e != nil {
			write(w, nil, e)
			return
		}
		v, e := a.Store.ConfirmCompanyDecision(r.Context(), user(r), identity, in.MaskName, in.Company, in.JobIDs, in.Selected, in.Key)
		decisionWriteError(w, v, e)
	})
}
func decodeJourney(r *http.Request, v any) error {
	b, e := io.ReadAll(io.LimitReader(r.Body, 1024*1024+1))
	if e != nil {
		return e
	}
	if len(b) > 1024*1024 {
		return p.ErrValidation
	}
	return d.StrictLimit(b, v, 1024*1024)
}
func decisionWriteError(w http.ResponseWriter, v any, e error) {
	w.Header().Set("Cache-Control", "no-store")
	write(w, v, e)
}
