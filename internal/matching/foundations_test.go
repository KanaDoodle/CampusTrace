package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestFoundationAssessmentsSupplyScopedKnowledgeWithoutInventingPractice(t *testing.T) {
	p := d.Profile{FoundationSkills: []d.FoundationSkill{{Topic: "ALGORITHMS", Level: "UNDERSTAND"}}}
	c, err := CandidateFrom(p, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var proof Fact
	for _, f := range c.Facts {
		if strings.HasPrefix(f.ID, "foundation-") {
			proof = f
		}
	}
	if proof.Kind != "SKILL" || !strings.Contains(proof.Text, "算法：了解（个人自评") {
		t.Fatal(proof)
	}
	for _, tc := range []struct{ text, result, note string }{
		{"了解算法", "DIRECT", ""}, {"熟悉算法", "PARTIAL", ContextUnconfirmed}, {"具备扎实的算法基础", "PARTIAL", ContextUnconfirmed}, {"有算法开发经验", "PARTIAL", ExperienceUnconfirmed},
	} {
		req := Requirement{ID: "r", Category: "REQUIRED", Aspect: "TECHNICAL", Text: tc.text, Excerpt: tc.text, Confidence: 1}
		in := ChatJob{Requirements: []Requirement{req}, Matches: []Match{{RequirementID: "r", Result: "DIRECT", Explanation: "基础能力自述相关", Evidence: []Citation{{ID: proof.ID, Excerpt: proof.Text}}}}}
		got, err := ImportChatJob(in, tc.text, d.Job{}, p, c, time.Time{})
		if err != nil || got.Matches[0].Result != tc.result || got.Matches[0].ReviewNote != tc.note || got.Matches[0].Evidence[0].Excerpt != proof.Text {
			t.Fatal(tc, got, err)
		}
	}
	p.FoundationSkills[0].Level = "FAMILIAR"
	changed, _ := CandidateFrom(p, nil, "")
	if changed.Hash() == c.Hash() {
		t.Fatal("changed assessment reused old analysis")
	}
	local := NewLocalScreener(p, changed).Screen(d.Job{Title: "后端开发"}, "工作要求:\n熟悉算法")
	if len(local.Checks) != 1 || local.Checks[0].Result != "SIGNAL" {
		t.Fatal("foundation not used in local screening", local)
	}
}
