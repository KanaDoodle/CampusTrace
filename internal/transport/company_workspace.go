package transport

import (
	"net/http"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

type companyWorkspaceRequest struct {
	matchPreviewRequest
	Company string   `json:"company"`
	JobIDs  []string `json:"job_ids"`
}

func (a *API) companyWorkspaceRoutes(on func(string, http.HandlerFunc)) {
	on("GET /api/matching/company-catalog", func(w http.ResponseWriter, r *http.Request) {
		company := strings.TrimSpace(r.URL.Query().Get("company"))
		if len(company) > 1024 {
			write(w, nil, p.ErrValidation)
			return
		}
		if company != "" {
			v, e := a.Store.DecisionCatalog(r.Context(), user(r), company)
			if e != nil {
				matchFailure(w, e)
				return
			}
			decisionWrite(w, v)
			return
		}
		v, e := a.Store.DecisionCompanies(r.Context(), user(r))
		if e != nil {
			write(w, nil, e)
			return
		}
		if len(v) > 1000 {
			matchFailure(w, matching.ErrDecisionCapacity)
			return
		}
		decisionWrite(w, v)
	})
	on("POST /api/matching/company-workspace", a.companyWorkspace)
	on("POST /api/matching/evaluation/export", a.exportMatchingEvaluation)
}

func (a *API) workspaceSnapshot(r *http.Request, in companyWorkspaceRequest) (p.MatchSnapshot, string, error) {
	in.Company = strings.TrimSpace(in.Company)
	if in.Company == "" || len(in.Company) > 1024 || len(in.JobIDs) > matching.MaxDecisionJobs {
		return p.MatchSnapshot{}, "", p.ErrValidation
	}
	identity, err := a.matchIdentity(in.matchPreviewRequest)
	if err != nil {
		return p.MatchSnapshot{}, "", err
	}
	v, err := a.Store.MatchWorkspaceSnapshot(r.Context(), user(r), identity, strings.TrimSpace(in.MaskName), in.JobIDs, in.Company)
	return v, identity, err
}

func (a *API) companyWorkspace(w http.ResponseWriter, r *http.Request) {
	var in companyWorkspaceRequest
	if err := decode(r, &in); err != nil {
		write(w, nil, err)
		return
	}
	snapshot, identity, err := a.workspaceSnapshot(r, in)
	if err != nil {
		matchFailure(w, err)
		return
	}
	scope := "ALL"
	if len(in.JobIDs) > 0 {
		scope = "SELECTED"
	}
	report := matching.BuildCompanyComparison(strings.TrimSpace(in.Company), scope, decisionInputs(snapshot), snapshot.Profile, time.Now().UTC())
	if err := a.Store.AttachCompanyReport(r.Context(), user(r), snapshot, strings.TrimSpace(in.Company), identity, &report, strings.TrimSpace(in.MaskName)); err != nil {
		matchFailure(w, err)
		return
	}
	decisionWrite(w, struct {
		Comparison matching.CompanyComparison `json:"comparison"`
		Workflow   *p.CompanyWorkflow         `json:"workflow"`
	}{report, snapshot.CompanyWorkflow})
}
