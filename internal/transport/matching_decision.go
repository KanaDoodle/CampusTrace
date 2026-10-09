package transport

import (
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"net/http"
	"strings"
	"time"
)

func decisionInputs(s p.MatchSnapshot) []matching.DecisionInput {
	out := make([]matching.DecisionInput, 0, len(s.Jobs))
	for _, j := range s.Jobs {
		out = append(out, matching.DecisionInput{Job: j.Job, State: j.State, ExcludedReason: j.ExcludedReason, Local: j.Local, Result: j.Result})
	}
	return out
}
func decisionWrite(w http.ResponseWriter, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		write(w, nil, err)
		return
	}
	if len(raw) > 2*1024*1024 {
		matchFailure(w, matching.ErrDecisionCapacity)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
func (a *API) compareCompany(w http.ResponseWriter, r *http.Request) {
	var in struct {
		matchPreviewRequest
		Company string   `json:"company"`
		JobIDs  []string `json:"job_ids"`
		Scope   string   `json:"scope"`
	}
	if err := decode(r, &in); err != nil {
		write(w, nil, err)
		return
	}
	company := strings.TrimSpace(in.Company)
	if company == "" || len(company) > 1024 || (in.Scope != "" && in.Scope != "ALL" && in.Scope != "FILTERED" && in.Scope != "SELECTED") {
		write(w, nil, p.ErrValidation)
		return
	}
	if len(in.JobIDs) > matching.MaxDecisionJobs {
		matchFailure(w, matching.ErrDecisionCapacity)
		return
	}
	if in.Scope != "" && in.Scope != "ALL" && len(in.JobIDs) == 0 {
		write(w, nil, p.ErrValidation)
		return
	}
	identity, err := a.matchIdentity(in.matchPreviewRequest)
	if err != nil {
		write(w, nil, err)
		return
	}
	snapshot, err := a.Store.MatchDecisionSnapshot(r.Context(), user(r), identity, strings.TrimSpace(in.MaskName), in.JobIDs, company)
	if err != nil {
		matchFailure(w, err)
		return
	}
	scope := "ALL"
	if len(in.JobIDs) > 0 {
		scope = "FILTERED"
		if in.Scope == "SELECTED" {
			scope = "SELECTED"
		}
	}
	report := matching.BuildCompanyComparison(company, scope, decisionInputs(snapshot), snapshot.Profile, time.Now().UTC())
	if err := a.Store.AttachCompanyReport(r.Context(), user(r), snapshot, company, identity, &report, strings.TrimSpace(in.MaskName)); err != nil {
		matchFailure(w, err)
		return
	}
	decisionWrite(w, report)
}
func (a *API) prepareMatchedJob(w http.ResponseWriter, r *http.Request) {
	var in matchPreviewRequest
	if err := decode(r, &in); err != nil {
		write(w, nil, err)
		return
	}
	identity, err := a.matchIdentity(in)
	if err != nil {
		write(w, nil, err)
		return
	}
	snapshot, err := a.Store.MatchDecisionSnapshot(r.Context(), user(r), identity, strings.TrimSpace(in.MaskName), []string{r.PathValue("id")}, "")
	if err != nil {
		matchFailure(w, err)
		return
	}
	plan := matching.BuildPreparation(decisionInputs(snapshot)[0], snapshot.Profile, time.Now().UTC())
	// Historical topics remain separately sourced; do not claim they are skill
	// gaps inferred from this job. Reading this view never changes review records.
	history, err := p.Many[d.WeakTopic](r.Context(), a.Store.DB, "SELECT body FROM weak_topics WHERE user_id=? ORDER BY CAST(JSON_EXTRACT(body,'$.weight') AS UNSIGNED)*CAST(JSON_EXTRACT(body,'$.occurrence_count') AS UNSIGNED) DESC,topic LIMIT 51", user(r))
	if err != nil {
		write(w, nil, err)
		return
	}
	truncated := len(history) > 50
	if truncated {
		history = history[:50]
	}
	decisionWrite(w, struct {
		matching.PreparationPlan
		History   []d.WeakTopic `json:"history_topics"`
		Truncated bool          `json:"history_truncated"`
	}{plan, history, truncated})
}
