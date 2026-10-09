package matching

import (
	"context"
	"errors"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func wholeFixture() (Candidate, HolisticJob, HolisticAssessment) {
	c := Candidate{Facts: []Fact{{ID: "go", Kind: "LANGUAGE", Text: "Go"}, {ID: "city", Kind: "CITY_PREFERRED", Text: "上海"}}, Projects: []CandidateProject{{ID: "p", Name: "任务服务", Description: "使用 Go 完成任务服务，利用事务与重试处理失败。", Bullets: []Fact{{ID: "bullet", Kind: "PROJECT_CONTEXT", Text: "通过日志与监控定位并发错误。", ProjectName: "任务服务"}}}}}
	j := HolisticJob{ID: "j", InputKey: "key", Company: "公司", Title: "后端", Text: "使用Go开发后端服务，关注可靠性与问题定位。热爱技术、团队协作。"}
	h := HolisticAssessment{Version: HolisticVersion, Fit: "RELATED", Summary: "服务开发和可靠性经验相关，可考虑投递。", CoreWork: "开发可靠的后端服务", Strengths: []HolisticFinding{{Point: "后端服务经验可以迁移", Explanation: "完整项目同时体现实现与可靠性处理，不推导线上规模。", JobExcerpt: "使用Go开发后端服务，关注可靠性与问题定位", Evidence: []Citation{{ID: "project-p-description", Excerpt: c.Projects[0].Description}}}}, Gaps: []HolisticFinding{}, Blockers: []HolisticFinding{}, Questions: []string{"岗位业务规模需核对"}, NextSteps: []string{"准备讲解故障处理取舍"}, IgnoredFactors: []string{"热爱技术、团队协作"}, Gates: []HolisticGate{}}
	return c, j, h
}
func TestWholeContextPreservesNarrativeAndRejectsFabricatedOrPreferenceProof(t *testing.T) {
	c, j, h := wholeFixture()
	if err := ValidateHolistic(h, j.Text, c); err != nil {
		t.Fatal(err)
	}
	// A coherent engineering capability can remain one key finding. It is not
	// copied into independent claims or forced into a 64-item scoring checklist.
	for _, variant := range []string{"fabricated", "preference", "wrong-job", "missing"} {
		copy := h
		copy.Strengths = append([]HolisticFinding{}, h.Strengths...)
		switch variant {
		case "fabricated":
			copy.Strengths[0].Evidence = []Citation{{ID: "project-p-description", Excerpt: "线上百万用户"}}
		case "preference":
			copy.Strengths[0].Evidence = []Citation{{ID: "city", Excerpt: "上海"}}
		case "wrong-job":
			copy.Strengths[0].JobExcerpt = "并不存在的岗位要求"
		case "missing":
			copy.Strengths[0].Evidence = nil
		}
		if err := ValidateHolistic(copy, j.Text, c); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %s: %v", variant, err)
		}
	}
}
func TestWholeModelReceivesUnsplitCandidateAndJDWithoutExtractionStage(t *testing.T) {
	c, j, h := wholeFixture()
	m := &fakeModel{output: map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}}}}
	results, err := AnalyzeHolistically(context.Background(), m, c, []HolisticJob{j})
	if err != nil || m.calls != 1 || results[j.ID].Fit != "RELATED" {
		t.Fatal(results, err, m.calls)
	}
	m.output = map[string]any{"jobs": []HolisticJobReply{{ID: "different", Assessment: h}}}
	if _, err := AnalyzeHolistically(context.Background(), m, c, []HolisticJob{j}); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-job result accepted", err)
	}
}
func TestWholeSavedNarrativeIsRedactedAndAllEditsInvalidateInput(t *testing.T) {
	profile := d.Profile{Educations: []d.Education{{ID: "b", Degree: "BACHELOR", Majors: []string{"计算机"}, GraduationYear: 2024}, {ID: "m", Degree: "MASTER", Majors: []string{"软件工程"}, GraduationYear: 2027}}}
	projects := []d.Project{{ID: "p", Name: "张同学的服务", Description: "联系 person@example.com，张同学实现任务服务", Bullets: []string{"手机13800138000，保留完整机制与结果"}}}
	c, err := CandidateWithProjects(profile, nil, projects, "张同学")
	if err != nil {
		t.Fatal(err)
	}
	wire := d.JSON(c)
	for _, s := range []string{"张同学", "person@example.com", "13800138000"} {
		if strings.Contains(wire, s) {
			t.Fatal("sensitive text survived", s)
		}
	}
	if !strings.Contains(wire, "本科") || !strings.Contains(wire, "硕士") || !strings.Contains(wire, "保留完整机制与结果") {
		t.Fatal("narrative or education lost", wire)
	}
	projects[0].Description += "，补充实现范围"
	next, err := CandidateWithProjects(profile, nil, projects, "张同学")
	if err != nil || next.Hash() == c.Hash() {
		t.Fatal("narrative edit did not invalidate input", err)
	}
}
func TestCompanyIdentityBindsCompleteScopeAndIgnoresInputOrder(t *testing.T) {
	c, j, _ := wholeFixture()
	k := j
	k.ID = "k"
	k.InputKey = "key2"
	k.Title = "平台"
	before := CompanyInputKey(c, []HolisticJob{j, k}, "model")
	if CompanyInputKey(c, []HolisticJob{k, j}, "model") != before {
		t.Fatal("selection order changed comparison identity")
	}
	for _, change := range []string{"scope", "text", "model", "project", "metadata"} {
		cc := c
		jj := j
		kk := k
		jobs := []HolisticJob{jj, kk}
		model := "model"
		switch change {
		case "scope":
			jobs = jobs[:1]
		case "text":
			jobs[1].Text += "新的职责"
		case "model":
			model = "other"
		case "project":
			cc.Projects = append([]CandidateProject{}, c.Projects...)
			cc.Projects[0].Description += "修改"
		case "metadata":
			jobs[1].Locations = []string{"杭州"}
		}
		if CompanyInputKey(cc, jobs, model) == before {
			t.Fatal("cache identity missed", change)
		}
	}
}
func TestWholeCapacityStopsBeforeModelAndKeepsEveryJob(t *testing.T) {
	c, j, _ := wholeFixture()
	j.Text = strings.Repeat("x", MaxBatchText+1)
	m := &fakeModel{}
	_, err := AnalyzeHolistically(context.Background(), m, c, []HolisticJob{j})
	var capacity *CapacityError
	if !errors.As(err, &capacity) || m.calls != 0 || capacity.Reason != "JOB_BYTES" {
		t.Fatal(err, m.calls)
	}
	c, j, _ = wholeFixture()
	jobs := []HolisticJob{j}
	for i := 0; i < MaxCompanyJobs; i++ {
		copy := j
		copy.ID = d.ID()
		jobs = append(jobs, copy)
	}
	if err := ValidateHolisticInput(c, jobs, true); !errors.As(err, &capacity) {
		t.Fatal("unbounded company request accepted", err)
	}
}
func TestCompanyRankingMustBindEachJobAndSupportKeyQuotes(t *testing.T) {
	c, j, _ := wholeFixture()
	k := j
	k.ID = "k"
	choice := CompanyChoice{ID: j.ID, Rank: 1, Reason: "更贴近服务可靠性", Advantage: "相关项目可迁移", Tradeoff: "业务规模需核对", JobExcerpt: "开发后端服务", Evidence: []Citation{{ID: "go", Excerpt: "Go"}}}
	other := choice
	other.ID = k.ID
	other.Rank = 2
	r := HolisticCompanyReport{Version: HolisticVersion, Company: j.Company, Summary: "先考虑服务开发", Choices: []CompanyChoice{choice, other}, Questions: []string{}}
	if err := ValidateCompanyReport(r, c, []HolisticJob{j, k}); err != nil {
		t.Fatal(err)
	}
	r.Choices[1].ID = j.ID
	if err := ValidateCompanyReport(r, c, []HolisticJob{j, k}); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate rank scope accepted", err)
	}
	r.Choices[1] = other
	r.Choices[1].Rank = 3
	if err := ValidateCompanyReport(r, c, []HolisticJob{j, k}); !errors.Is(err, ErrInvalid) {
		t.Fatal("missing rank group accepted", err)
	}
}
