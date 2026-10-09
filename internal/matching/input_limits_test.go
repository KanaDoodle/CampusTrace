package matching

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestCompleteProjectEvidenceHasRoomForSerializationOverhead(t *testing.T) {
	facts := []d.ProjectFact{}
	for i := 0; i < 32; i++ {
		facts = append(facts, d.ProjectFact{ID: fmt.Sprintf("%032x", i), ProjectID: "project", Kind: "IMPLEMENTED", Verified: true, Claim: strings.Repeat("实现任务处理与失败重试，", 10)})
	}
	c, err := CandidateWithProjects(d.Profile{Languages: []string{"Go"}}, facts, []d.Project{{ID: "project", Name: "包含完整实现机制和结果的任务队列"}}, "")
	if err != nil || len(d.JSON(c)) <= 12000 || len(c.Facts) != 34 || c.Facts[2].Text != facts[0].Claim {
		t.Fatalf("complete evidence still fails the old budget or was truncated: bytes=%d facts=%d err=%v", len(d.JSON(c)), len(c.Facts), err)
	}
	facts[0].Claim = strings.Repeat("private synthetic text ", MaxCandidateText/20+1)
	c, err = CandidateWithProjects(d.Profile{}, facts, nil, "")
	var capacity *CapacityError
	if !errors.Is(err, ErrCapacity) || !errors.As(err, &capacity) || capacity.Reason != "CANDIDATE_BYTES" || capacity.Actual != len(d.JSON(c)) || capacity.Limit != MaxCandidateText || strings.Contains(err.Error(), "private synthetic") {
		t.Fatal("capacity error lost safe, exact input bounds", err)
	}
}

func TestComparisonPackingAccountsForCompleteCandidateAndJobBytes(t *testing.T) {
	c := Candidate{Facts: []Fact{}}
	for i := 0; i < 8; i++ {
		c.Facts = append(c.Facts, Fact{ID: fmt.Sprint(i), Kind: "IMPLEMENTED", Text: strings.Repeat("x", 3600)})
	}
	jobs := []MatchInput{}
	for i := 0; i < 3; i++ {
		job := MatchInput{ID: fmt.Sprint(i), Requirements: []Requirement{}}
		for j := 0; j < 5; j++ {
			job.Requirements = append(job.Requirements, Requirement{ID: fmt.Sprint(j), Category: "REQUIRED", Text: strings.Repeat("需求", 200), Excerpt: strings.Repeat("原文", 80), Confidence: 1})
		}
		jobs = append(jobs, job)
	}
	if ComparisonBytes(c, jobs[:2]) > MaxComparisonText || ComparisonBytes(c, jobs) <= MaxComparisonText || ComparisonBatchSize(c, jobs) != 2 {
		t.Fatalf("byte-heavy jobs were not split: two=%d three=%d count=%d", ComparisonBytes(c, jobs[:2]), ComparisonBytes(c, jobs), ComparisonBatchSize(c, jobs))
	}
	m := &fakeModel{}
	_, err := Compare(context.Background(), m, c, jobs)
	var capacity *CapacityError
	if !errors.As(err, &capacity) || capacity.Reason != "COMPARISON_BYTES" || capacity.Actual != ComparisonBytes(c, jobs) || m.calls != 0 {
		t.Fatal("over-budget comparison reached the model or lost its reason", err)
	}
	if ComparisonBatchSize(c, jobs[2:]) != 1 || ComparisonBatchSize(c, nil) != 0 {
		t.Fatal("packing did not retain the remaining complete job")
	}
}

func TestComparisonPackingKeepsTheOutputRequirementBudget(t *testing.T) {
	jobs := []MatchInput{{ID: "a", Requirements: make([]Requirement, 13)}, {ID: "b", Requirements: make([]Requirement, 12)}}
	if ComparisonBatchSize(Candidate{}, jobs) != 1 {
		t.Fatal("input byte headroom removed the output size guard")
	}
}
