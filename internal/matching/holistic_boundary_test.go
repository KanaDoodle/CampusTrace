package matching

import (
	"context"
	"errors"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// These exercise the active model boundary after retiring the former
// extraction/comparison protocol, rather than preserving a second executor.
func TestWholeModelRejectsWrongJobScopeAndSchemaWithoutRetry(t *testing.T) {
	c, j, h := wholeFixture()
	k := j
	k.ID, k.Text = "other", "维护独立的数据平台，使用SQL分析业务数据。"
	for _, tc := range []struct {
		name   string
		output any
	}{
		{"unknown job", map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}, {ID: "private-unknown", Assessment: h}}}},
		{"duplicate job", map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}, {ID: j.ID, Assessment: h}}}},
		{"missing job", map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}}}},
		{"cross job quote", map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}, {ID: k.ID, Assessment: h}}}},
		{"unknown field", map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}, {ID: k.ID, Assessment: h}}, "private-extra": true}},
		{"null jobs", map[string]any{"jobs": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := &fakeModel{output: tc.output}
			got, err := AnalyzeHolistically(context.Background(), model, c, []HolisticJob{j, k})
			if got != nil || !errors.Is(err, ErrInvalid) || model.calls != 1 {
				t.Fatal("invalid response accepted or retried", err, model.calls)
			}
			if strings.Contains(err.Error(), "private-") {
				t.Fatal("diagnostic echoed untrusted output")
			}
		})
	}
}

func TestWholeModelCannotUsePreferencesOrLimitsAsAchievements(t *testing.T) {
	for _, kind := range []string{"CITY_PREFERRED", "ROLE", "JOB_TYPE_PREFERENCE", "LIMITATION", "PLANNED"} {
		t.Run(kind, func(t *testing.T) {
			c, j, h := wholeFixture()
			c.Facts = append(c.Facts, Fact{ID: "boundary", Kind: kind, Text: "private-source：相关服务经验"})
			h.Strengths[0].Evidence = []Citation{{ID: "boundary", Excerpt: "相关服务经验"}}
			model := &fakeModel{output: map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}}}}
			got, err := AnalyzeHolistically(context.Background(), model, c, []HolisticJob{j})
			if got != nil || !errors.Is(err, ErrInvalid) || model.calls != 1 || strings.Contains(err.Error(), "private-source") {
				t.Fatal("non-ability material became an achievement", err)
			}
		})
	}
}

func TestWholeModelRejectsInvalidInputBeforeAnyCall(t *testing.T) {
	c, j, _ := wholeFixture()
	oversized := j
	oversized.Text = strings.Repeat("x", MaxBatchText+1)
	for _, jobs := range [][]HolisticJob{nil, {j, j}, {oversized}, {{ID: "empty", Text: " \n"}}} {
		model := &fakeModel{}
		if _, err := AnalyzeHolistically(context.Background(), model, c, jobs); err == nil || model.calls != 0 {
			t.Fatal("invalid input reached model", err, model.calls)
		}
	}
}

func TestWholeRequestBudgetCountsCompleteCandidateAndJobs(t *testing.T) {
	c, j, _ := wholeFixture()
	c.Projects[0].Description = strings.Repeat("x", MaxCandidateText-100)
	jobs := []HolisticJob{}
	for _, id := range []string{"a", "b", "c"} {
		copy := j
		copy.ID, copy.Text = id, strings.Repeat("y", 20000)
		jobs = append(jobs, copy)
	}
	model := &fakeModel{}
	_, err := AnalyzeHolistically(context.Background(), model, c, jobs)
	var capacity *CapacityError
	actual := len(d.JSON(HolisticRequest{Version: HolisticVersion, Candidate: ReviewedCandidate(c), Jobs: jobs}))
	if !errors.As(err, &capacity) || capacity.Reason != "HOLISTIC_BYTES" || capacity.Actual != actual || capacity.Limit != MaxHolisticInput || model.calls != 0 {
		t.Fatal("complete input was truncated or its budget ignored", err, model.calls)
	}
}
