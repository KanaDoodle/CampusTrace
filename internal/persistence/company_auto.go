package persistence

import (
	"context"
	"sort"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

type AutoCompanyCandidate struct {
	JobID  string `json:"job_id"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

type AutoCompanySelection struct {
	Company  string                 `json:"company"`
	AsOf     time.Time              `json:"as_of"`
	Total    int                    `json:"total"`
	Eligible int                    `json:"eligible"`
	Selected []AutoCompanyCandidate `json:"selected"`
	Skipped  map[string]int         `json:"skipped"`
	Deferred int                    `json:"deferred"`
}

// Candidate selection is local and read-only. A recommendation is produced only
// by the existing, separately confirmed whole-context comparison.
func (s *Store) AutoCompanyCandidates(ctx context.Context, user, model, mask, company string) (AutoCompanySelection, error) {
	snapshot, err := s.MatchCompanyCandidates(ctx, user, model, mask, company)
	if err != nil {
		return AutoCompanySelection{}, err
	}
	return autoCompanyCandidates(snapshot, strings.TrimSpace(company), time.Now().UTC()), nil
}

func autoCompanyCandidates(snapshot MatchSnapshot, company string, now time.Time) AutoCompanySelection {
	out := AutoCompanySelection{Company: company, AsOf: now, Selected: []AutoCompanyCandidate{}, Skipped: map[string]int{}}
	fit := func(row MatchJob) int {
		if row.State != "ANALYZED" || row.Holistic == nil {
			return 2
		}
		switch row.Holistic.Fit {
		case "STRONG":
			return 4
		case "RELATED":
			return 3
		case "WEAK":
			return 1
		}
		return 2
	}
	direction := func(row MatchJob) int {
		if row.Local != nil {
			switch row.Local.Direction.Status {
			case "MATCH":
				return 3
			case "RELATED":
				return 2
			case "UNRELATED":
				return 0
			}
		}
		return 1 // Uncertain direction is still eligible for the model comparison.
	}
	eligible := []MatchJob{}
	for _, row := range snapshot.Jobs {
		if row.Job.Company != company {
			continue
		}
		out.Total++
		reason := ""
		switch {
		case row.Job.CurrentStatus == "CLOSED":
			reason = "closed"
		case row.Disposition == "IGNORED":
			reason = "ignored"
		case row.Application != nil && (row.Application.AppliedAt != nil || row.Application.State != "PLANNED"):
			reason = "applied"
		case row.Campaign != nil && row.Campaign.HideUnsubmitted:
			reason = "quota"
		case row.ExcludedReason != "" || strings.TrimSpace(row.Text) == "":
			reason = "unavailable"
		case direction(row) == 0 && fit(row) < 3:
			reason = "unrelated"
		case len(row.Text) > matching.MaxBatchText:
			reason = "text_limit"
		}
		if reason != "" {
			out.Skipped[reason]++
			continue
		}
		eligible = append(eligible, row)
	}
	out.Eligible = len(eligible)
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if fit(a) != fit(b) {
			return fit(a) > fit(b)
		}
		if direction(a) != direction(b) {
			return direction(a) > direction(b)
		}
		if a.PreliminaryScore != b.PreliminaryScore {
			return a.PreliminaryScore > b.PreliminaryScore
		}
		return a.Job.ID < b.Job.ID
	})
	// Identical JD copies do not consume all slots before different work has had
	// a chance to enter the comparison. No JD is truncated to satisfy the budget.
	unique, duplicates := []MatchJob{}, []MatchJob{}
	seen := map[string]bool{}
	for _, row := range eligible {
		key := d.Hash(row.Text)
		if seen[key] {
			duplicates = append(duplicates, row)
		} else {
			unique = append(unique, row)
			seen[key] = true
		}
	}
	jobs := []matching.HolisticJob{}
	// JSON array entries add independently; count the reviewed candidate once so
	// an oversized document does not get serialized for every unselected job.
	size := len(d.JSON(matching.HolisticRequest{Version: matching.HolisticVersion, Candidate: matching.ReviewedCandidate(snapshot.Candidate), Jobs: jobs}))
	if size > matching.MaxCompanyInput {
		out.Deferred = out.Eligible
		return out
	}
	for _, row := range append(unique, duplicates...) {
		if len(jobs) == matching.MaxCompanyJobs {
			break
		}
		candidate := WholeJobs([]MatchJob{row})[0]
		added := len(d.JSON(candidate))
		if len(jobs) > 0 {
			added++
		}
		if size+added > matching.MaxCompanyInput {
			continue
		}
		proposed := append(append([]matching.HolisticJob{}, jobs...), candidate)
		if matching.ValidateHolisticInput(snapshot.Candidate, proposed, true) != nil {
			continue
		}
		jobs = proposed
		size += added
		why := "方向尚待判断，保留给整体比较核对"
		if direction(row) == 3 {
			why = "与主投方向一致"
		} else if direction(row) == 2 {
			why = "与主投方向相关"
		}
		if fit(row) == 4 {
			why = "当前整体分析为优先，纳入同公司取舍"
		} else if fit(row) == 3 {
			why = "当前整体分析为相关，纳入同公司取舍"
		}
		out.Selected = append(out.Selected, AutoCompanyCandidate{JobID: row.Job.ID, Title: row.Job.Title, Reason: why})
	}
	out.Deferred = out.Eligible - len(out.Selected)
	return out
}
