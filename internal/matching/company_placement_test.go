package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"testing"
)

func TestTechnicalCompanyPlacementKeepsTiesScopeAndUnknowns(t *testing.T) {
	row := func(id, company, state string, direct, missing int) PriorityInput {
		return PriorityInput{Job: d.Job{ID: id, Company: company, Title: "后端开发"}, State: state, Coverage: float64(direct) / float64(direct+missing) * 100, Breakdown: []SectionScore{{Category: "REQUIRED", Total: direct + missing, Known: direct, Direct: direct, Missing: missing}}}
	}
	a, b := row("a", "A", "ANALYZED", 1, 2), row("b", "A", "ANALYZED", 1, 2)
	lower := row("lower", "A", "ANALYZED", 1, 3)
	stale, pending := row("stale", "A", "STALE", 4, 0), row("pending", "A", "BASIC", 4, 0)
	closed := row("closed", "A", "ANALYZED", 4, 0)
	closed.Job.CurrentStatus = "CLOSED"
	ignored := row("ignored", "A", "ANALYZED", 4, 0)
	ignored.ExcludedReason = "ignored"
	unknown := row("unknown", "A", "ANALYZED", 0, 4)
	other := row("other", "B", "ANALYZED", 4, 0)
	got := TechnicalCompanyPlacements([]PriorityInput{b, other, lower, pending, stale, closed, ignored, unknown, a}, d.Profile{})
	if len(got) != 4 || got["a"].Rank != 1 || !got["a"].Tied || got["b"].Rank != 1 || got["lower"].Rank != 3 || got["a"].Total != 3 || got["a"].Pending != 2 || got["other"].Total != 1 {
		t.Fatal(got)
	}
}
