package matching

import (
	"context"
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
	"testing"
	"time"
)

type fakeModel struct {
	output any
	input  string
	calls  int
}

func (m *fakeModel) Complete(_ context.Context, messages any, _ any) (json.RawMessage, error) {
	m.calls++
	var input []map[string]string
	if json.Unmarshal([]byte(d.JSON(messages)), &input) == nil && len(input) > 1 {
		m.input = input[1]["content"]
	}
	return json.Marshal(map[string]string{"content": d.JSON(m.output)})
}

func TestCandidateUsesOnlyConfirmedFactsAndMasksIdentifiers(t *testing.T) {
	p := d.Profile{UserID: "private-account", Revision: 2, Degree: "MASTER", Languages: []string{"Go"}, Skills: []string{"Redis"}}
	facts := []d.ProjectFact{{ID: "yes", Kind: "IMPLEMENTED", Verified: true, Claim: "张同学用 Go 实现重试，邮箱 test@example.com"}, {ID: "pending", Kind: "IMPLEMENTED", Claim: "未核对的项目"}, {ID: "plan", Kind: "PLANNED", Verified: true, Claim: "计划支持 Kafka"}, {ID: "limit", Kind: "LIMITATION", Verified: true, Claim: "没有真实生产流量"}}
	c, err := CandidateWithProjects(p, facts, nil, "张同学")
	if err != nil {
		t.Fatal(err)
	}
	text := d.JSON(c)
	for _, absent := range []string{"private-account", "test@example.com", "张同学", "未核对", "计划支持"} {
		if strings.Contains(text, absent) {
			t.Fatalf("outbound leak %q", absent)
		}
	}
	if !strings.Contains(text, "实现重试") || !strings.Contains(text, "没有真实生产流量") {
		t.Fatal(text)
	}
	changed := facts
	changed[0].Claim = "实现退避重试"
	next, _ := CandidateWithProjects(p, changed, nil, "")
	if c.Hash() == next.Hash() {
		t.Fatal("fact edits did not invalidate match")
	}
}

func TestCitationsCannotInventSkillsOrTurnLimitationsIntoAchievements(t *testing.T) {
	c := Candidate{Facts: []Fact{{ID: "go", Kind: "LANGUAGE", Text: "Go"}, {ID: "limit", Kind: "LIMITATION", Text: "没有生产部署"}, {ID: "role", Kind: "ROLE", Text: "后端开发"}}}
	reqs := []Requirement{{ID: "r", Category: "REQUIRED", Confidence: 1}}
	valid := []Match{{RequirementID: "r", Result: "PARTIAL", Explanation: "语言有依据，生产经验需要补充", Evidence: []Citation{{"go", "Go"}}}}
	if err := ValidateMatches(c, reqs, valid); err != nil {
		t.Fatal(err)
	}
	for _, citation := range []Citation{{"go", "Kafka"}, {"missing", "Go"}, {"limit", "没有生产部署"}, {"role", "后端开发"}} {
		m := valid[0]
		m.Evidence = []Citation{citation}
		if ValidateMatches(c, reqs, []Match{m}) == nil {
			t.Fatalf("bad positive citation accepted %+v", citation)
		}
	}
	m := valid[0]
	m.Result = "MISMATCH"
	m.Evidence = nil
	if ValidateMatches(c, reqs, []Match{m}) == nil {
		t.Fatal("missing information became rejection")
	}
}
func TestMissingEvidenceReducesCoverageAndSuppressesUnreliableScores(t *testing.T) {
	reqs := []Requirement{{ID: "r", Category: "REQUIRED", Confidence: 1}, {ID: "b", Category: "BONUS", Confidence: 1}}
	score, coverage := Score(reqs, []Match{{RequirementID: "r", Result: "NO_EVIDENCE"}, {RequirementID: "b", Result: "DIRECT"}})
	if score != nil || coverage != 0 {
		t.Fatal(score, coverage)
	}
	score, coverage = Score(reqs, []Match{{RequirementID: "r", Result: "PARTIAL"}, {RequirementID: "b", Result: "NO_EVIDENCE"}})
	if score == nil || *score != 50 || coverage != 100 {
		t.Fatal(score, coverage)
	}
}
func TestLocalScreeningRetainsUnknownRequirementsAndNormalizesCitiesAndRoles(t *testing.T) {
	p := d.Profile{GraduationYear: 2027, Degree: "MASTER", TargetRoles: []string{"后端开发"}, PreferredCities: []string{"Shanghai"}, PreferredTypes: []string{"FULL_TIME"}, Languages: []string{"Go"}}
	j := d.Job{Title: "服务端开发工程师", Locations: []string{"上海市"}, JobType: "FULL_TIME", UpdatedAt: time.Now()}
	for _, tc := range []struct {
		text, degree string
		excluded     bool
	}{
		{"岗位要求尚不明确", "MASTER", false},
		{"degree: PHD\ngraduation: 2027", "MASTER", true},
		{"必须掌握 C++，Go 可作为加分项", "MASTER", false},
		{"面向2026届、2027届毕业生", "MASTER", false},
		{"硕士及以上优先", "BACHELOR", false},
	} {
		profile := p
		profile.Degree = tc.degree
		candidate, err := CandidateWithProjects(profile, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		got := NewLocalScreener(profile, candidate).Screen(j, tc.text)
		if (got.ExcludedReason != "") != tc.excluded {
			t.Fatal(tc, got)
		}
		if tc.text == "岗位要求尚不明确" && got.Score != 50 {
			t.Fatal(got)
		}
	}
	if RequirementKey("same JD", "model-a") == RequirementKey("same JD", "model-b") {
		t.Fatal("model not in cache identity")
	}
}
