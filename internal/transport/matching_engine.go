package transport

import (
	"context"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"strings"
	"time"
)

type matchBatchInput struct {
	JobIDs                  []string
	CandidateHash, MaskName string
	Company, ScopeKey       string
}
type MatchStageError struct {
	Stage string
	Err   error
}

func (e *MatchStageError) Error() string { return "matching stage failed: " + e.Stage }
func (e *MatchStageError) Unwrap() error { return e.Err }

type matchBatchOutcome struct {
	Analyzed           []string `json:"analyzed"`
	Reused             []string `json:"reused"`
	RequirementsReused int      `json:"requirements_reused"`
	Calls              int      `json:"calls"`
	EvidenceReviews    int      `json:"evidence_reviews"`
}

func (a *API) executeMatchBatch(ctx context.Context, user string, in matchBatchInput, model resume.Completer, identity string, hook func(string, string, int) error) (out matchBatchOutcome, err error) {
	snapshot, err := a.Store.MatchSnapshot(ctx, user, identity, strings.TrimSpace(in.MaskName), in.JobIDs)
	if err != nil {
		return out, err
	}
	if in.CandidateHash != snapshot.CandidateHash {
		return out, p.ErrStaleInput
	}
	keys := map[string]string{}
	remaining := []p.MatchJob{}
	for _, job := range snapshot.Jobs {
		keys[job.Job.ID] = job.InputKey
		if job.State == "ANALYZED" && job.AnalysisMode == matching.HolisticVersion {
			out.Reused = append(out.Reused, job.Job.ID)
			continue
		}
		if job.ExcludedReason != "" {
			return out, p.ErrMatchJobUnavailable
		}
		remaining = append(remaining, job)
	}
	if err := a.Store.CheckMatchBatchInputs(ctx, user, keys); err != nil {
		return out, err
	}
	budget := &budgetedMatchModel{model: model, store: a.Store, user: user}
	defer func() {
		out.Calls = budget.calls
		if a.Metrics != nil {
			a.Metrics.Add("matching_model_calls_total", float64(budget.calls))
			a.Metrics.Add("matching_result_cache_hits_total", float64(len(out.Reused)))
		}
	}()
	if len(remaining) == 0 {
		return out, nil
	}
	jobs := p.WholeJobs(remaining, in.MaskName)
	if err := matching.ValidateHolisticInput(snapshot.Candidate, jobs, false); err != nil {
		return out, &MatchStageError{"PREPARE", err}
	}
	if hook != nil {
		if err := hook("ANALYZE", "", 0); err != nil {
			return out, err
		}
	}
	results, err := matching.AnalyzeHolistically(ctx, budget, snapshot.Candidate, jobs)
	if err != nil {
		return out, &MatchStageError{"ANALYZE", err}
	}
	for _, job := range remaining {
		assessment := results[job.Job.ID]
		reqs, err := matching.HolisticRequirements(assessment, job.Text)
		if err != nil {
			return out, &MatchStageError{"ANALYZE", err}
		}
		result := matching.Result{QualityVersion: matching.QualityVersion, Holistic: &assessment, JobID: job.Job.ID, InputKey: job.InputKey, RequirementsKey: job.RequirementsKey, CandidateHash: snapshot.CandidateHash, Model: identity, AnalyzedAt: time.Now().UTC(), Requirements: reqs, Matches: []matching.Match{}, CandidateFacts: snapshot.Candidate.Facts, Qualifications: matching.Qualification(job.Job, snapshot.Profile, reqs, time.Now().UTC()), Breakdown: []matching.SectionScore{}}
		if hook != nil {
			if err := hook("SAVE", "", 0); err != nil {
				return out, err
			}
		}
		if err := a.Store.SaveMatchResult(ctx, user, strings.TrimSpace(in.MaskName), result); err != nil {
			return out, &MatchStageError{"SAVE", err}
		}
		out.Analyzed = append(out.Analyzed, job.Job.ID)
		if hook != nil {
			if err := hook("SAVE", job.Job.ID, 0); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}

func (a *API) executeCompanyBatch(ctx context.Context, user string, in matchBatchInput, model resume.Completer, identity string, hook func(string, string, int) error) (out matchBatchOutcome, err error) {
	snapshot, err := a.Store.MatchSnapshot(ctx, user, identity, in.MaskName, in.JobIDs)
	if err != nil {
		return out, err
	}
	jobs := p.WholeJobs(snapshot.Jobs, in.MaskName)
	if snapshot.CandidateHash != in.CandidateHash || in.ScopeKey != matching.CompanyInputKey(snapshot.Candidate, jobs, identity) {
		return out, p.ErrStaleInput
	}
	for _, row := range snapshot.Jobs {
		if row.Job.Company != in.Company || row.ExcludedReason != "" {
			return out, p.ErrMatchJobUnavailable
		}
	}
	if err := matching.ValidateHolisticInput(snapshot.Candidate, jobs, true); err != nil {
		return out, &MatchStageError{"PREPARE", err}
	}
	if cached, e := a.Store.CompanyReport(ctx, user, in.Company, in.ScopeKey); e == nil && cached.Version == matching.HolisticVersion {
		for _, j := range jobs {
			out.Reused = append(out.Reused, j.ID)
		}
		return out, nil
	} else if e != nil && !errors.Is(e, p.ErrNotFound) {
		return out, e
	}
	budget := &budgetedMatchModel{model: model, store: a.Store, user: user}
	defer func() {
		out.Calls = budget.calls
		if a.Metrics != nil {
			a.Metrics.Add("matching_company_model_calls_total", float64(budget.calls))
		}
	}()
	if hook != nil {
		if err := hook("COMPANY", "", 0); err != nil {
			return out, err
		}
	}
	report, err := matching.CompareHolistically(ctx, budget, snapshot.Candidate, jobs, identity)
	if err != nil {
		return out, &MatchStageError{"COMPANY", err}
	}
	if hook != nil {
		if err := hook("SAVE", "", 0); err != nil {
			return out, err
		}
	}
	report.Company = in.Company
	if err := a.Store.SaveCompanyReport(ctx, user, in.MaskName, report, jobs); err != nil {
		return out, &MatchStageError{"SAVE", err}
	}
	for _, j := range jobs {
		out.Analyzed = append(out.Analyzed, j.ID)
		if hook != nil {
			if err := hook("SAVE", j.ID, 0); err != nil {
				return out, err
			}
		}
	}
	return out, nil
}
