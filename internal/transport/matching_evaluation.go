package transport

import (
	"net/http"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matcheval"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func (a *API) exportMatchingEvaluation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		companyWorkspaceRequest
		ScopeKey  string              `json:"expected_scope_key"`
		Reference matcheval.Reference `json:"reference"`
	}
	if err := decode(r, &in); err != nil {
		write(w, nil, err)
		return
	}
	if len(in.JobIDs) == 0 || len(in.JobIDs) > matching.MaxCompanyJobs || len(in.ScopeKey) != 64 || !in.Reference.Reviewed {
		write(w, nil, p.ErrValidation)
		return
	}
	snapshot, identity, err := a.workspaceSnapshot(r, in.companyWorkspaceRequest)
	if err != nil {
		matchFailure(w, err)
		return
	}
	for _, j := range snapshot.Jobs {
		if j.ExcludedReason != "" {
			matchFailure(w, p.ErrStaleInput)
			return
		}
	}
	jobs := p.WholeJobs(snapshot.Jobs, strings.TrimSpace(in.MaskName))
	key := matching.CompanyInputKey(snapshot.Candidate, jobs, identity)
	if key != in.ScopeKey {
		matchFailure(w, p.ErrStaleInput)
		return
	}
	if err := matcheval.ValidateReference(in.Reference, jobs); err != nil {
		write(w, nil, p.ErrValidation)
		return
	}
	if err := matching.ValidateHolisticInput(snapshot.Candidate, jobs, true); err != nil {
		matchFailure(w, err)
		return
	}
	id := d.Hash(key + "\n" + d.JSON(in.Reference))
	// Keep the sanitized local source index for citation validation. The model
	// still receives ReviewedCandidate through CompareHolistically, only once.
	pack := matcheval.Dataset{Version: matcheval.Version, Cases: []matcheval.Case{{ID: id, Name: strings.TrimSpace(in.Company) + " · 同公司取舍", Source: "USER_REVIEWED_SNAPSHOT", Candidate: snapshot.Candidate, Jobs: jobs, Reference: in.Reference}}, Recorded: []matcheval.Trial{}}
	report := matching.BuildCompanyComparison(strings.TrimSpace(in.Company), "SELECTED", decisionInputs(snapshot), snapshot.Profile, snapshot.CompanyWorkflow.AsOf)
	if err := a.Store.AttachCompanyReport(r.Context(), user(r), snapshot, strings.TrimSpace(in.Company), identity, &report, strings.TrimSpace(in.MaskName)); err != nil {
		matchFailure(w, err)
		return
	}
	if report.Holistic != nil {
		pack.Recorded = append(pack.Recorded, matcheval.Trial{CaseID: id, Attempt: 1, Model: report.Holistic.Model, Report: report.Holistic})
	}
	decisionWrite(w, pack)
}
