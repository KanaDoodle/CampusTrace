package matching

import (
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestDirectionTriageUsesResponsibilitiesAndPreservesAmbiguity(t *testing.T) {
	s := localFixture(t, localProfile(), nil)
	for _, tc := range []struct{ title, text, want string }{
		{"服务端开发", "任职要求\n熟悉 Java", "MATCH"},
		{"Backend Engineer", "Responsibilities\nDevelop backend services", "MATCH"},
		{"研发工程师", "岗位职责\n负责后端服务开发", "MATCH"},
		{"Software Engineer", "Responsibilities\nDesign backend services", "MATCH"},
		{"软件工程师", "岗位职责\n负责 API 接口开发\n设计数据库与异步任务", "MATCH"},
		{"中间件研发工程师", "岗位职责\n开发中间件", "RELATED"},
		{"Full Stack Engineer", "岗位职责\n负责前后端开发", "RELATED"},
		{"数据工程师", "岗位职责\n建设数据仓库", "RELATED"},
		{"前端开发", "岗位职责\n负责前端开发\n负责后端服务开发", "UNCERTAIN"},
		{"后端与算法工程师", "岗位职责\n负责算法研究和后端开发", "UNCERTAIN"},
		{"研发工程师", "任职要求\n熟悉 Go 和 Redis", "UNCERTAIN"},
		{"Product Engineer", "任职要求\n熟悉任意一种编程语言", "UNCERTAIN"},
		{"研发工程师", "公司介绍\n负责后端服务开发\n加分项\n后端开发经验优先", "UNCERTAIN"},
		{"营销专员", "岗位职责\n负责市场推广\n加分项\nGo 后端经验优先", "UNRELATED"},
		{"机械工程师", "岗位职责\n设计机械结构\n任职要求\n有编程经验", "UNRELATED"},
		{"软件测试工程师", "岗位职责\n负责软件测试", "UNRELATED"},
		{"会计", "岗位职责\n负责会计核算", "UNRELATED"},
		{"HR Specialist", "Responsibilities\nManage recruitment", "UNRELATED"},
		{"岗位尚未公布", "岗位要求尚未公布", "UNCERTAIN"},
		{"前端开发", "岗位职责\n" + strings.Repeat("负责前端研发工作", 50), "UNCERTAIN"},
	} {
		t.Run(tc.title+"/"+tc.want, func(t *testing.T) {
			v := s.Screen(d.Job{Title: tc.title}, tc.text).Direction
			if v.Status != tc.want {
				t.Fatalf("%s: %+v", tc.text, v)
			}
			for _, e := range v.Evidence {
				if e.Source == "TITLE" && !strings.Contains(tc.title, e.Excerpt) || e.Source == "BODY" && !strings.Contains(tc.text, e.Excerpt) {
					t.Fatal("direction evidence is not verbatim", e)
				}
			}
		})
	}
}

func TestDirectionDoesNotUseScoreSkillsCityOrModelAndSupportsMultipleTargets(t *testing.T) {
	p := localProfile()
	job := d.Job{Title: "服务端开发", Locations: []string{"北京"}}
	before := localFixture(t, p, nil).Screen(job, "任职要求\n熟悉 C++ 和 Java")
	p.Skills, p.Languages, p.PreferredCities = []string{"Python"}, nil, []string{"广州"}
	after := localFixture(t, p, nil).Screen(job, "任职要求\n熟悉 C++ 和 Java")
	if before.Direction.Status != "MATCH" || after.Direction.Status != "MATCH" || after.ExcludedReason != "" {
		t.Fatal(before, after)
	}
	p.TargetRoles = []string{"后端开发", "前端开发"}
	if got := localFixture(t, p, nil).Screen(d.Job{Title: "前端开发"}, "岗位职责\n负责前端开发"); got.Direction.Status != "MATCH" {
		t.Fatal(got.Direction)
	}
	for _, targets := range [][]string{nil, {"软件研发"}, {"后端开发", "自定义职能"}} {
		p.TargetRoles = targets
		v := localFixture(t, p, nil).Screen(d.Job{Title: "会计"}, "岗位职责\n负责会计核算").Direction
		if v.Status != "UNCERTAIN" {
			t.Fatal("unsupported or empty preference caused dismissal", targets, v)
		}
	}
	p.TargetRoles = []string{"机械工程师"}
	if got := localFixture(t, p, nil).Screen(d.Job{Title: "机械结构工程师"}, "岗位职责\n设计机械结构"); got.Direction.Status != "MATCH" {
		t.Fatal(got.Direction)
	}
}
