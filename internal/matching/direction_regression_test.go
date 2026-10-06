package matching

import (
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestAgentTargetsRecognizeMainAdjacentAndClearlyDifferentWork(t *testing.T) {
	p := localProfile()
	p.TargetRoles = []string{"backend", "Agent开发", "服务端"}
	s := localFixture(t, p, nil)
	for _, tc := range []struct{ title, text, want string }{
		{"服务端开发工程师", "任职要求\n熟悉 Go", "MATCH"},
		{"Agent开发工程师", "任职要求\n熟悉 Go", "MATCH"},
		{"北京-Agent Harness 研发工程师", "工作职责\n参与设计与开发 Agent Harness 系统", "MATCH"},
		{"AI 应用开发工程师", "任职要求\n熟悉 Python", "MATCH"},
		{"研究员-大模型应用", "岗位职责\n构建智能体应用", "MATCH"},
		{"AI Infra工程师", "技能素质要求\n熟悉 Go", "RELATED"},
		{"AI安全研发工程师", "任职要求\n熟悉 Go", "RELATED"},
		{"大数据引擎研发工程师", "岗位职责\n建设大数据计算引擎服务", "RELATED"},
		{"大数据平台开发工程师", "岗位职责\n建设数据平台", "RELATED"},
		{"Linux内核研发工程师", "任职要求\n掌握 C", "RELATED"},
		{"大模型推理系统工程师", "任职要求\n熟悉 C++", "RELATED"},
		{"软件开发工程师", "任职要求\n熟悉任意一种编程语言", "RELATED"},
		{"Software Engineer", "Responsibilities\nDevelop backend services", "MATCH"},
		{"软件研发工程师", "职位描述（官网原文）：\n负责前端开发\n任职要求\n熟悉 JavaScript", "UNRELATED"},
		{"软件研发工程师", "职位职责\n负责 API 接口开发\n设计数据库\n任职要求\n熟悉 Go", "MATCH"},
		{"Java研发工程师", "任职要求\n熟悉 Java", "RELATED"},
		{"C++开发工程师", "任职要求\n熟悉 C++", "RELATED"},
		{"Product Engineer", "任职要求\n熟悉任意一种编程语言", "UNCERTAIN"},
		{"财务会计", "任职要求\n熟悉 SQL", "UNRELATED"},
		{"招聘专员", "岗位职责\n负责招聘专员的培训", "UNRELATED"},
		{"电机工程师", "岗位职责\n设计电机", "UNRELATED"},
		{"电芯研发工程师", "岗位职责\n参与磷酸铁锂体系技术研究与研发平台建设", "UNRELATED"},
		{"标准情报工程师", "岗位职责\n进行法规符合性评估", "UNRELATED"},
		{"游戏客户端开发工程师", "任职要求\n熟悉 Go 和 Redis", "UNRELATED"},
		{"多模态后训练及Agent能力拓展研究员", "任职要求\n熟悉 Python", "UNRELATED"},
		{"市场营销策划", "岗位职责\n使用 AI 工具辅助营销\n任职要求\n有编程经验优先", "UNRELATED"},
		{"宏观研究员", "岗位职责\n开展宏观研究\n构建 AI 应用系统", "UNCERTAIN"},
		{"前端与后端开发工程师", "岗位职责\n负责前端开发与后端开发", "UNCERTAIN"},
		{"研发工程师", "任职要求\n熟悉 Go 和 Redis", "UNCERTAIN"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			v := s.Screen(d.Job{Title: tc.title}, tc.text)
			if v.Direction.Status != tc.want || v.ExcludedReason != "" {
				t.Fatal(v.Direction, v.ExcludedReason)
			}
			for _, e := range v.Direction.Evidence {
				source := tc.text
				if e.Source == "TITLE" {
					source = tc.title
				}
				if !strings.Contains(source, e.Excerpt) {
					t.Fatal("direction citation was rewritten", e)
				}
			}
		})
	}
}

func TestDirectionCompletenessIsIndependentOfSkillsAndGraduationParsing(t *testing.T) {
	s := localFixture(t, localProfile(), nil)
	for _, tc := range []struct{ title, text, want string }{
		{"前端开发工程师", "岗位职责\n负责前端开发\n任职要求\n2026年9月至2027年8月毕业生", "UNRELATED"},
		{"营销专员", "岗位职责\n负责市场推广\n任职要求\n" + strings.Repeat("熟悉业务流程与数据分析", 35), "UNRELATED"},
		{"前端开发工程师", "岗位职责\n" + strings.Repeat("负责前端研发工作", 50), "UNCERTAIN"},
		{"前端开发工程师", "岗位职责\n" + strings.Repeat("负责前端开发\n", 65) + "负责后端开发", "UNCERTAIN"},
	} {
		t.Run(tc.title+tc.want, func(t *testing.T) {
			parsed := parseLocal(tc.text)
			v := s.Screen(d.Job{Title: tc.title}, tc.text)
			if !parsed.Incomplete || v.Direction.Status != tc.want || v.ExcludedReason != "" {
				t.Fatal("partial skills/gates changed direction or became a hard rejection", v.Direction, parsed)
			}
		})
	}
}

func TestDirectionDoesNotUseMetadataIntroductionsCollaborationOrBonusAsOwnDuties(t *testing.T) {
	s := localFixture(t, localProfile(), nil)
	for _, tc := range []struct{ title, text, want string }{
		{"岗位尚未公布", "岗位名称：后端开发工程师\n工作地点：软件研发中心\n公司介绍\n负责后端服务开发", "UNCERTAIN"},
		{"研发工程师", "团队介绍\n团队负责后端开发\n【加分项】\n后端开发经验优先", "UNCERTAIN"},
		{"后端开发工程师", "岗位职责\n与前端开发团队协作\n负责后端服务开发\n任职要求\n熟悉前端开发框架", "MATCH"},
		{"市场营销策划", "职位职责\n负责营销策划\n技能素质要求\n熟悉 Agent 开发经验优先", "UNRELATED"},
		{"研发工程师", "Your role:\nDevelop backend services\nWhat you bring:\nExperience with frontend development", "MATCH"},
		{"研发工程师", "职位描述：\n负责后端研发，具备服务设计经验\n任职要求\n熟悉 Go", "MATCH"},
		{"后端开发工程师", "岗位职责\n负责业务分析和技术方案设计\n负责服务端开发", "MATCH"},
		{"后端开发工程师(AI方向)", "岗位职责\nAI 应用开发：参与经营分析和资金管理等AI应用的设计与开发", "RELATED"},
		{"CodeAgent AI工程师", "工作职责\n【愿景】\n打造下一代智能应用\n【团队介绍】\n负责模型训练\n【你将负责】\n负责 API 接口开发\n设计数据库\n【任职资格】\n熟悉 Python", "RELATED"},
		{"研发工程师", "工作职责\n【团队介绍】\n负责前端开发\n【你将负责】\n负责后端服务开发\n任职资格\n熟悉 Java", "MATCH"},
		{"研发工程师", "职位描述\n熟悉后端开发经验\n具备 MySQL 使用经验", "UNCERTAIN"},
	} {
		v := s.Screen(d.Job{Title: tc.title}, tc.text)
		if v.Direction.Status != tc.want {
			t.Fatal(tc.title, v.Direction)
		}
	}
}

func TestLocalPriorityAndCompanyComparisonUseTheSameDirection(t *testing.T) {
	p := localProfile()
	p.TargetRoles = []string{"backend", "Agent开发"}
	s := localFixture(t, p, nil)
	for _, tc := range []struct {
		title string
		want  int
	}{
		{"后端开发工程师", 2}, {"Agent Harness 研发工程师", 2}, {"AI Infra工程师", 1}, {"软件工程师", 1}, {"会计", -1}, {"后端与算法工程师", 0},
	} {
		j := d.Job{Title: tc.title}
		local := s.Screen(j, "任职要求\n熟悉 Go")
		pref, _ := directionPreference(p, DecisionInput{Job: j, Local: &local})
		if pref != tc.want {
			t.Fatal(tc.title, pref, local.Direction)
		}
		if tc.want == 2 && local.Score < 60 || tc.want < 1 && strings.Contains(local.Reasons[0], "主投方向一致") {
			t.Fatal("priority calculation disagrees with direction", tc.title, local)
		}
	}
}

func TestExpandedHeadingsRetainBonusScopeAndConservativeQualifications(t *testing.T) {
	v := localFixture(t, localProfile(), nil).Screen(localJob(), "职位职责\n负责后端服务开发\n【加分项】\n熟悉 C++\n具备博士及以上学历")
	if v.Direction.Status != "MATCH" || v.ExcludedReason != "" || len(v.Checks) != 1 || v.Checks[0].Category != "BONUS" {
		t.Fatal("a preferred skill/degree became mandatory", v)
	}
}
