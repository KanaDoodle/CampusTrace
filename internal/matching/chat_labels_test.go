package matching

import (
	"errors"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func coveredSoftFixture() (ChatJob, string, d.Profile, Candidate) {
	text := "负责直播系统架构设计、研发与维护，与产品、设计、测试团队紧密协作，高效推进业务迭代与需求落地"
	p := d.Profile{Languages: []string{"Go"}}
	c, _ := CandidateFrom(p, nil, "")
	c.Facts = append(c.Facts, Fact{ID: "rpc", Kind: "IMPLEMENTED", Text: "实现 Go RPC 请求复用与持久任务恢复"})
	in := ChatJob{Requirements: []Requirement{
		{ID: "soft", Category: "RESPONSIBILITY", Aspect: "SOFT", Text: "协作", Excerpt: text, Confidence: 1},
		{ID: "duty", Category: "RESPONSIBILITY", Aspect: "TECHNICAL", Text: text, Excerpt: text, Confidence: 1},
	}, Matches: []Match{
		{RequirementID: "soft", Result: "NO_EVIDENCE", Explanation: "未记录团队协作", Evidence: []Citation{}},
		{RequirementID: "duty", Result: "TRANSFERABLE", Explanation: "后端机制可迁移，直播业务待了解", Evidence: []Citation{{ID: "rpc", Excerpt: "Go RPC 请求复用与持久任务恢复"}}},
	}}
	return in, text, p, c
}

func TestChatCoveredSoftClauseNarrowsExistingDutyWithoutCopyingJudgment(t *testing.T) {
	in, source, p, c := coveredSoftFixture()
	got, err := ImportChatJob(in, source, d.Job{}, p, c, time.Now())
	if err != nil || len(got.Requirements) != 2 || len(got.Matches) != 2 {
		t.Fatal(got, err)
	}
	if strings.Contains(got.Requirements[1].Text, "协作") || !strings.Contains(got.Requirements[1].Text, "系统架构设计、研发与维护") || got.Requirements[1].Excerpt != source || got.Matches[1].Result != "TRANSFERABLE" || got.Matches[0].Result != "NO_EVIDENCE" || got.Score != nil {
		t.Fatal(got)
	}
	if in.Requirements[1].Text != source {
		t.Fatal("input document mutated")
	}
}

func TestChatEngineeringDiagnosisKeepsItsContextAndPartialEvidence(t *testing.T) {
	for _, method := range []string{"工程化的排查思路", "工程化排查思路", "工程化的排查方法"} {
		t.Run(method, func(t *testing.T) {
			text := "具备清晰的逻辑思维和" + method + "，能借助日志、监控、trace等手段深入问题本质，而非停留在表面修补"
			p := d.Profile{}
			c, _ := CandidateFrom(p, nil, "")
			proof := Fact{ID: "race", Kind: "IMPLEMENTED", Text: "使用 Race Detector 验证并发请求路径"}
			c.Facts = append(c.Facts, proof)
			in := ChatJob{Requirements: []Requirement{
				{ID: "logic", Category: "RESPONSIBILITY", Aspect: "SOFT", Text: "逻辑思维", Excerpt: text, Confidence: 1},
				{ID: "diagnosis", Category: "RESPONSIBILITY", Aspect: "TECHNICAL", Text: text, Excerpt: text, Confidence: 1},
			}, Matches: []Match{
				{RequirementID: "logic", Result: "NO_EVIDENCE", Explanation: "未记录该软性特质", Evidence: []Citation{}},
				{RequirementID: "diagnosis", Result: "PARTIAL", Explanation: "有并发检测实践，完整线上排障能力待了解", Evidence: []Citation{{ID: proof.ID, Excerpt: proof.Text}}},
			}}
			got, err := ImportChatJob(in, "工作要求:\n"+text, d.Job{}, p, c, time.Now())
			if err != nil || len(got.Requirements) != 2 || len(got.Matches) != 2 {
				t.Fatal(got, err)
			}
			if got.Requirements[1].Text != text || got.Requirements[1].Excerpt != text || got.Requirements[1].Aspect != "TECHNICAL" || got.Matches[1].Result != "PARTIAL" || d.JSON(got.Matches[1].Evidence) != d.JSON(in.Matches[1].Evidence) || got.Matches[0].Result != "NO_EVIDENCE" || got.Breakdown[0].Total != 1 || got.Breakdown[3].Total != 1 {
				t.Fatal("coherent diagnostic ability was split or its evidence expanded", got)
			}
			in.Matches[1].Evidence[0].Excerpt = "使用线上监控定位全部故障"
			if _, err := ImportChatJob(in, "工作要求:\n"+text, d.Job{}, p, c, time.Now()); err == nil {
				t.Fatal("coherent context bypassed personal citation checks")
			}
		})
	}
}

func TestChatSoftRepairRequiresIndependentSameSourceFacetAndKeepsValidation(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		change       func(*ChatJob)
	}{
		{"missing facet", "CHAT_REQUIREMENT_COMPOSITE", func(in *ChatJob) { in.Requirements = in.Requirements[1:]; in.Matches = in.Matches[1:] }},
		{"another source", "CHAT_REQUIREMENT_COMPOSITE", func(in *ChatJob) { in.Requirements[0].Excerpt = "其他职责：与团队协作" }},
		{"different category", "CHAT_REQUIREMENT_COMPOSITE", func(in *ChatJob) { in.Requirements[0].Category = "REQUIRED" }},
		{"partial facet", "CHAT_REQUIREMENT_COMPOSITE", func(in *ChatJob) { in.Requirements[0].Text = "沟通" }},
		{"invalid citation", "EXCERPT_NOT_EXACT", func(in *ChatJob) { in.Matches[1].Evidence[0].Excerpt = "GoRPC实现" }},
		{"duplicate match", "MATCH_COUNT", func(in *ChatJob) { in.Matches = append(in.Matches, in.Matches[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, source, p, c := coveredSoftFixture()
			tc.change(&in)
			_, err := ImportChatJob(in, source+"\n其他职责：与团队协作", d.Job{}, p, c, time.Now())
			var v *ValidationError
			if !errors.As(err, &v) || v.Reason != tc.reason {
				t.Fatal(v, err)
			}
		})
	}
	for _, text := range []string{"熟悉操作系统、网络、数据结构与算法", "热爱技术，熟练掌握 Go"} {
		in, _, p, c := coveredSoftFixture()
		in.Requirements[1].Category = "REQUIRED"
		in.Requirements[1].Text = text
		in.Requirements[1].Excerpt = text
		_, err := ImportChatJob(in, in.Requirements[0].Excerpt+"\n"+text, d.Job{}, p, c, time.Now())
		var v *ValidationError
		if !errors.As(err, &v) || v.Reason != "CHAT_REQUIREMENT_COMPOSITE" {
			t.Fatal("true independent requirements were accepted", err)
		}
	}
	text := "维护 Go 系统，与团队协作开发软件"
	items := []Requirement{{ID: "tech", Category: "RESPONSIBILITY", Text: text, Excerpt: text}, {ID: "soft", Category: "RESPONSIBILITY", Aspect: "SOFT", Text: "协作", Excerpt: text}}
	if removeCoveredSoftClauses(items)[0].Text != text {
		t.Fatal("a technical task was removed with collaboration")
	}
}

func TestChatSectionRestorationIsBoundToUnambiguousExactSource(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
		count  int
	}{
		{"工作职责:\n维护服务\n工作要求:\n熟悉 Go\n加分项:\n熟悉 Redis", "REQUIRED", 1},
		{"工作职责:\n熟悉 Go\n工作要求:\n熟悉 Go", "RESPONSIBILITY", 0},
		{"工作职责:\n熟悉 Go", "RESPONSIBILITY", 0},
		{"熟悉 Go", "RESPONSIBILITY", 0},
		{"加分项:\n熟悉 Go", "BONUS", 1},
	} {
		r := Requirement{ID: "go", Category: "RESPONSIBILITY", Text: "熟悉 Go", Excerpt: "熟悉 Go", Confidence: 1}
		got, count := restoreChatCategories([]Requirement{r}, tc.source)
		if got[0].Category != tc.want || count != tc.count || r.Category != "RESPONSIBILITY" {
			t.Fatal(got, count, tc)
		}
	}
	items := []Requirement{{Category: "QUALIFICATION", Excerpt: "本科及以上学历"}, {Category: "RESPONSIBILITY", GroupID: "any", Excerpt: "熟悉 Go"}, {Category: "RESPONSIBILITY", Excerpt: "维护服务\n工作要求:\n熟悉 Go"}}
	got, count := restoreChatCategories(items, "工作职责:\n维护服务\n工作要求:\n本科及以上学历\n熟悉 Go")
	if count != 0 || got[0].Category != "QUALIFICATION" {
		t.Fatal("qualifications/groups/boundaries were inferred", got, count)
	}
	parsed := parseLocal("工作职责:\n维护服务\n工作要求:\n熟悉 Go")
	if len(parsed.Requirements) != 1 || parsed.Requirements[0].Category != "REQUIRED" {
		t.Fatal(parsed)
	}
}

func TestChatLanguageUmbrellaRemovalRetainsPreciseOwnMatch(t *testing.T) {
	quote := "熟练掌握 Go、Java、C++ 中至少一种语言"
	p := d.Profile{Languages: []string{"Go"}}
	c, _ := CandidateFrom(p, nil, "")
	in := ChatJob{Requirements: []Requirement{
		{ID: "generic", Category: "RESPONSIBILITY", Text: "掌握至少一种/一门岗位列举的主流编程语言", Excerpt: quote, Confidence: 1},
		{ID: "precise", Category: "RESPONSIBILITY", Text: quote, Excerpt: quote, Confidence: 1},
	}, Matches: []Match{
		{RequirementID: "generic", Result: "DIRECT", Explanation: "已填写语言", Evidence: []Citation{{ID: "language-0", Excerpt: "Go"}}},
		{RequirementID: "precise", Result: "PARTIAL", Explanation: "相关语言，熟练程度待核对", Evidence: []Citation{{ID: "language-0", Excerpt: "Go"}}},
	}}
	got, err := ImportChatJob(in, "工作要求:\n"+quote, d.Job{}, p, c, time.Now())
	if err != nil || got.RestoredCategories != 2 || len(got.Requirements) != 1 || got.Requirements[0].ID != "precise" || len(got.Matches) != 1 || got.Matches[0].Result != "PARTIAL" || got.Score == nil || *got.Score != 50 {
		t.Fatal(got, err)
	}
	in.Matches[0].Evidence[0].Excerpt = "Golang"
	if _, err = ImportChatJob(in, "工作要求:\n"+quote, d.Job{}, p, c, time.Now()); err == nil {
		t.Fatal("ignored restatement bypassed citation checks")
	}
	for _, label := range []string{"熟悉 Go、Java、C++", "有 Go、Java、C++ 中至少一种语言开发经验", "熟练掌握 Go、Java、C++ 中至少一种语言和 Redis"} {
		items := append([]Requirement{}, in.Requirements...)
		items[1].Text = label
		if len(coveredLanguageUmbrellas(items)) > 0 {
			t.Fatal("an independent condition was discarded", label)
		}
	}
}

func TestRestoredAcademicGateNeverBecomesTechnicalScore(t *testing.T) {
	text := "2027届本科及以上学历"
	p := d.Profile{Degree: "MASTER", GraduationYear: 2027}
	c, _ := CandidateFrom(p, nil, "")
	in := ChatJob{Requirements: []Requirement{{ID: "academic", Category: "RESPONSIBILITY", Text: text, Excerpt: text, Confidence: 1}}, Matches: []Match{{RequirementID: "academic", Result: "NO_EVIDENCE", Explanation: "原模型未核对资格", Evidence: []Citation{}}}}
	got, err := ImportChatJob(in, "工作要求:\n"+text, d.Job{}, p, c, time.Now())
	if err != nil || got.Requirements[0].Category != "QUALIFICATION" || got.Score != nil || got.Breakdown[0].Total != 0 || got.Qualifications.Status == "ELIGIBLE" {
		t.Fatal(got, err)
	}
	for _, label := range []string{"专业能力：熟练掌握 Go", "具有专业的软件开发能力", "熟练掌握 Go", "开发学历认证服务"} {
		items, count := restoreChatCategories([]Requirement{{Category: "RESPONSIBILITY", Text: label, Excerpt: label}}, "工作要求:\n"+label)
		if count != 1 || items[0].Category != "REQUIRED" {
			t.Fatal("technical ability became an academic gate", items)
		}
	}
	in.Requirements[0].Text = "2027届本科及以上学历，计算机相关专业优先"
	in.Requirements[0].Excerpt = in.Requirements[0].Text
	got, err = ImportChatJob(in, "工作要求:\n"+in.Requirements[0].Text, d.Job{}, p, c, time.Now())
	if err != nil || got.Requirements[0].Category != "QUALIFICATION" || got.Score != nil {
		t.Fatal("preferred major demoted a mandatory degree", got, err)
	}
}
