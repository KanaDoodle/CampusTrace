package matching

import (
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
