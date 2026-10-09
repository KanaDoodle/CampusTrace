package matching

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
	"testing"
)

func TestReviewedCandidateDocumentPreservesParagraphsAndSendsOneCopy(t *testing.T) {
	extra := "实习：实现服务鉴权。\n科研：完成检索实验，尚无生产部署。"
	c, err := CandidateWithProjects(d.Profile{AdditionalExperience: &extra, Languages: []string{"Go"}, Educations: []d.Education{{ID: "b", Degree: "BACHELOR", GraduationYear: 2024}, {ID: "m", Degree: "MASTER", GraduationYear: 2027}}}, []d.ProjectFact{{ID: "planned", Kind: "PLANNED", Verified: true, Claim: "未实现的秘密计划"}}, []d.Project{{ID: "p", Name: "后端项目", Description: "使用 Go 实现服务。\n通过事务处理并发。", Bullets: []string{"实现队列、重试与幂等，保留整体描述。"}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	doc := ReviewedDocument(c)
	for _, v := range []string{extra, "本科", "硕士", "使用 Go 实现服务。\n通过事务处理并发。", "实现队列、重试与幂等，保留整体描述。"} {
		if !strings.Contains(doc, v) {
			t.Fatal("missing complete content", v)
		}
	}
	if strings.Contains(doc, "未实现的秘密计划") {
		t.Fatal("planned fact leaked")
	}
	c0, j, h := wholeFixture()
	m := &fakeModel{output: map[string]any{"jobs": []HolisticJobReply{{ID: j.ID, Assessment: h}}}}
	if _, err := AnalyzeHolistically(context.Background(), m, c0, []HolisticJob{j}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.input, `"projects"`) || strings.Contains(m.input, `"facts"`) || !strings.Contains(m.input, `"document"`) {
		t.Fatal("model did not receive a single readable document", m.input)
	}
	if strings.Count(m.input, c0.Projects[0].Description) != 1 {
		t.Fatal("project duplicated in paid context")
	}
}
