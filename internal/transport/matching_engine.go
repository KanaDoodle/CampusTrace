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
	for _, j := range snapshot.Jobs {
		keys[j.Job.ID] = j.InputKey
	}
	if err := a.Store.CheckMatchBatchInputs(ctx, user, keys); err != nil {
		return out, err
	}
	remaining := []p.MatchJob{}
	reused := []string{}
	total := 0
	for _, job := range snapshot.Jobs {
		if job.State == "ANALYZED" {
			reused = append(reused, job.Job.ID)
			continue
		}
		if job.ExcludedReason != "" {
			return out, p.ErrMatchJobUnavailable
		}
		if job.TextBytes == 0 || job.TextBytes > matching.MaxBatchText {
			return out, matching.ErrCapacity
		}
		total += job.TextBytes
		remaining = append(remaining, job)
	}
	if total > matching.MaxBatchText {
		return out, matching.ErrCapacity
	}
	budget := &budgetedMatchModel{model: model, store: a.Store, user: user}
	defer func() {
		out.Calls = budget.calls
		if a.Metrics != nil {
			a.Metrics.Add("matching_model_calls_total", float64(budget.calls))
			a.Metrics.Add("matching_result_cache_hits_total", float64(len(reused)))
		}
	}()
	requirements := map[string]matching.Requirements{}
	uncached := []matching.JobText{}
	representative := map[string]string{}
	requirementsReused := 0
	for _, job := range remaining {
		reqs, err := a.Store.CachedRequirements(ctx, user, job.RequirementsKey)
		if err == nil {
			requirements[job.Job.ID] = reqs
			requirementsReused++
			continue
		}
		if !errors.Is(err, p.ErrNotFound) {
			return out, err
		}
		if representative[job.RequirementsKey] == "" {
			representative[job.RequirementsKey] = job.Job.ID
			uncached = append(uncached, matching.JobText{ID: job.Job.ID, Text: job.Text})
		}
	}
	if a.Metrics != nil {
		a.Metrics.Add("matching_requirements_cache_hits_total", float64(requirementsReused))
	}
	if len(uncached) > 0 {
		if hook != nil {
			if err := hook("EXTRACT", "", 0); err != nil {
				return out, err
			}
		}
		extracted, err := matching.Extract(ctx, budget, uncached)
		if err != nil {
			return out, &MatchStageError{"EXTRACT", err}
		}
		for _, job := range remaining {
			if _, ok := requirements[job.Job.ID]; ok {
				continue
			}
			reqs := extracted[representative[job.RequirementsKey]]
			requirements[job.Job.ID] = reqs
			if err := a.Store.SaveRequirements(ctx, user, job.RequirementsKey, reqs); err != nil {
				return out, &MatchStageError{"SAVE", err}
			}
		}
	}
	inputs := []matching.MatchInput{}
	for _, job := range remaining {
		if len(requirements[job.Job.ID].Items) > 0 {
			inputs = append(inputs, matching.MatchInput{ID: job.Job.ID, Requirements: requirements[job.Job.ID].Items})
		}
	}
	analyzed := []string{}
	evidenceReviews := 0
	byID := map[string]p.MatchJob{}
	for _, job := range remaining {
		byID[job.Job.ID] = job
	}
	persist := func(job p.MatchJob, matches []matching.Match) error {
		started := time.Now()
		if a.Metrics != nil {
			defer func() { a.Metrics.Observe("matching_result_commit_seconds", time.Since(started).Seconds()) }()
		}
		reqs := requirements[job.Job.ID].Items
		if matches == nil {
			matches = []matching.Match{}
		}
		score, coverage := matching.Score(reqs, matches)
		result := matching.Result{JobID: job.Job.ID, InputKey: job.InputKey, RequirementsKey: job.RequirementsKey, CandidateHash: snapshot.CandidateHash, Model: identity, AnalyzedAt: time.Now().UTC(), Requirements: reqs, Matches: matches, CandidateFacts: snapshot.Candidate.Facts, Score: score, Coverage: coverage, Qualifications: matching.Qualification(job.Job, snapshot.Profile, reqs, time.Now().UTC())}
		result.Breakdown = matching.ScoreBreakdown(reqs, matches)
		if hook != nil {
			if err := hook("SAVE", "", 0); err != nil {
				return err
			}
		}
		if err := a.Store.SaveMatchResult(ctx, user, strings.TrimSpace(in.MaskName), result); err != nil {
			return err
		}
		analyzed = append(analyzed, job.Job.ID)
		if hook != nil {
			if err := hook("SAVE", job.Job.ID, matching.EvidenceReviewCount(matches)); err != nil {
				return err
			}
		}
		evidenceReviews += matching.EvidenceReviewCount(matches)
		return nil
	}
	for _, job := range remaining {
		if len(requirements[job.Job.ID].Items) == 0 {
			if err := persist(job, nil); err != nil {
				return out, &MatchStageError{"SAVE", err}
			}
		}
	}
	// Bound output as well as input: a dense JD must not crowd out another
	// job's requirements. Commit each completed group before reserving more calls.
	for len(inputs) > 0 {
		count, size := 0, 0
		for count < len(inputs) {
			next := len(inputs[count].Requirements)
			if count > 0 && size+next > 24 {
				break
			}
			size += next
			count++
		}
		group := inputs[:count]
		if hook != nil {
			if err := hook("COMPARE", "", 0); err != nil {
				return out, err
			}
		}
		comparisons, err := matching.Compare(ctx, budget, snapshot.Candidate, group)
		if err != nil {
			return out, &MatchStageError{"COMPARE", err}
		}
		for _, job := range group {
			if err := persist(byID[job.ID], comparisons[job.ID]); err != nil {
				return out, &MatchStageError{"SAVE", err}
			}
		}
		inputs = inputs[count:]
	}
	out.Analyzed, out.Reused, out.RequirementsReused, out.EvidenceReviews = analyzed, reused, requirementsReused, evidenceReviews
	return out, nil
}
