package matching

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func localFixture(t *testing.T, p d.Profile, facts []d.ProjectFact) *LocalScreener {
	t.Helper()
	c, err := CandidateFrom(p, facts, "")
	if err != nil {
		t.Fatal(err)
	}
	return NewLocalScreener(p, c)
}
func localProfile() d.Profile {
	return d.Profile{Degree: "MASTER", GraduationYear: 2027, TargetRoles: []string{"后端开发"}, Skills: []string{"Go", "Redis"}, PreferredCities: []string{"Shanghai"}, AcceptableCities: []string{"杭州"}, PreferredTypes: []string{"FULL_TIME"}}
}
func localJob() d.Job {
	return d.Job{Title: "服务端开发", JobType: "FULL_TIME", Locations: []string{"上海市"}}
}
func TestLocalVocabularyBoundariesAndAliases(t *testing.T) {
	for _, tc := range []struct {
		text string
		want []string
	}{
		{"Google JavaScript GitHub myredis goroutine sync.Mutex", []string{"javascript"}},
		{"C++ C# C golang Go Java JS TS Node.js", []string{"cpp", "csharp", "c", "go", "java", "javascript", "typescript", "nodejs"}},
		{"SpringBoot PostgreSQL Kubernetes", []string{"springboot", "postgresql", "kubernetes"}},
	} {
		if got := localFeatures(tc.text); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %v != %v", tc.text, got, tc.want)
		}
	}
}
func TestLocalAlternativesMandatoryBonusAndExactQuotes(t *testing.T) {
	s := localFixture(t, localProfile(), nil)
	for _, tc := range []struct{ text, result, mode string }{
		{"熟悉 Go, Java, C++ 任意一种语言", "SIGNAL", "ANY"},
		{"同时掌握 Go、Java、C++", "PARTIAL", "ALL"},
		{"同时掌握 Go/Java", "PARTIAL", "ALL"},
		{"熟悉 C++/C", "NO_EVIDENCE", "ANY"},
		{"熟悉任意一种编程语言", "SIGNAL", "ALL"},
		{"熟悉 MySQL 或 Redis 任选一种", "SIGNAL", "ANY"},
	} {
		v := s.Screen(localJob(), tc.text)
		if len(v.Checks) != 1 || v.Checks[0].Result != tc.result || v.Checks[0].Mode != tc.mode || v.Checks[0].Excerpt != tc.text {
			t.Errorf("%q: %+v", tc.text, v.Checks)
		}
	}
	v := s.Screen(localJob(), "必须掌握 C++，Go 优先")
	if len(v.Checks) != 2 || v.Checks[0].Category != "REQUIRED" || v.Checks[0].Result != "NO_EVIDENCE" || v.Checks[1].Category != "BONUS" || v.Checks[1].Result != "SIGNAL" || v.ExcludedReason != "" || v.Tier == "HIGH" {
		t.Fatal(v)
	}
	v = s.Screen(localJob(), "熟悉 Go/Java 任意一种，掌握 MySQL")
	if len(v.Checks) != 2 || v.Checks[1].Result != "NO_EVIDENCE" {
		t.Fatal(v)
	}
}
func TestLocalRoleFromResponsibilitiesAndGroundedProjects(t *testing.T) {
	facts := []d.ProjectFact{{ID: "impl", Kind: "IMPLEMENTED", Verified: true, Claim: "实现 Go 服务接口、Redis 任务队列和失败重试、幂等处理"}}
	s := localFixture(t, localProfile(), facts)
	j := localJob()
	j.Title = "研发工程师"
	text := "岗位职责\n负责服务接口开发\n实现数据库和异步任务处理、失败重试\n任职要求\n熟悉 Go 和 Redis"
	v := s.Screen(j, text)
	if v.Role != "后端开发" || v.RoleSource != "BODY" || !strings.Contains(text, v.RoleExcerpt) || v.Score <= 65 || v.Tier != "HIGH" {
		t.Fatal(v)
	}
	bad := s.Screen(localJob(), "必须掌握 C++，Go 优先")
	if v.Score <= bad.Score {
		t.Fatal("body/project evidence did not outrank misleading title", v, bad)
	}
	noProjects := localFixture(t, localProfile(), nil).Screen(j, text)
	if noProjects.Score >= v.Score {
		t.Fatal("confirmed implemented facts ignored", noProjects, v)
	}
	for _, c := range v.Checks {
		for _, e := range c.Evidence {
			if e.Kind == "IMPLEMENTED" && !strings.Contains(facts[0].Claim, e.Excerpt) {
				t.Fatal("invented fact citation", e)
			}
		}
	}
	j.Title = "前端开发"
	v = s.Screen(j, text)
	if v.Role != "前端开发" || v.Tier == "HIGH" {
		t.Fatal("body overrode explicit different title", v)
	}
	v = s.Screen(d.Job{Title: "研发工程师"}, "公司介绍\n负责 Go 后端接口和数据库开发")
	if v.Role != "" || len(v.Checks) != 0 {
		t.Fatal("company introduction became duties", v)
	}
}
func TestLocalCannotInventSkillsFromPlansLimitationsOrComponents(t *testing.T) {
	p := localProfile()
	p.Skills = []string{"Redis"}
	for _, fact := range []d.ProjectFact{
		{Kind: "PLANNED", Verified: true, Claim: "计划实现并发控制"},
		{Kind: "LIMITATION", Verified: true, Claim: "未实现并发控制"},
		{Kind: "IMPLEMENTED", Verified: false, Claim: "实现并发控制"},
		{Kind: "IMPLEMENTED", Verified: true, Claim: "未实现并发控制"},
		{Kind: "IMPLEMENTED", Verified: true, Claim: "使用 Redis"},
	} {
		v := localFixture(t, p, []d.ProjectFact{fact}).Screen(localJob(), "要求掌握并发控制")
		if len(v.Checks) != 1 || v.Checks[0].Result != "NO_EVIDENCE" {
			t.Fatal(fact, v)
		}
	}
	v := localFixture(t, p, nil).Screen(localJob(), "无需 Redis\n熟悉 Google\n掌握 goroutine 和 sync.Mutex")
	if len(v.Checks) != 0 || v.Tier != "UNCERTAIN" {
		t.Fatal(v)
	}
	p.Skills = nil
	p.Languages = []string{"英语"}
	if got := localFixture(t, p, nil).Screen(localJob(), "熟悉任意一种编程语言"); got.Checks[0].Result != "NO_EVIDENCE" {
		t.Fatal("English was treated as a programming language", got)
	}
	longFact := d.ProjectFact{ID: "long", Kind: "IMPLEMENTED", Verified: true, Claim: strings.Repeat("已实现数据处理", 40) + " Go 服务"}
	if got := localFixture(t, p, []d.ProjectFact{longFact}).Screen(localJob(), "熟悉 Go"); got.Checks[0].Result != "NO_EVIDENCE" {
		t.Fatal("skill outside bounded citation was counted", got)
	}
}
func TestLocalPreferencesDuplicateSkillsAndFreshness(t *testing.T) {
	p := localProfile()
	s := localFixture(t, p, nil)
	j := localJob()
	original := s.Screen(j, "熟悉 Go 和 Redis")
	p.Skills = append(p.Skills, "golang", "Go", "Redis")
	p.Languages = []string{"Go"}
	duplicate := localFixture(t, p, nil).Screen(j, "熟悉 Go 和 Redis")
	if original.Score != duplicate.Score {
		t.Fatal("duplicate aliases inflated score", original, duplicate)
	}
	p.Skills = append(p.Skills, "Go", "Go", "Go", "Go", "Go", "Go")
	projectEvidence := localFixture(t, p, []d.ProjectFact{{ID: "proof", Kind: "IMPLEMENTED", Verified: true, Claim: "实现 Go 服务"}}).Screen(j, "熟悉 Go")
	if projectEvidence.Checks[0].Evidence[0].ID != "proof" {
		t.Fatal("repeated skills hid project proof", projectEvidence)
	}
	j.UpdatedAt = time.Now().Add(-30 * 24 * time.Hour)
	if s.Screen(j, "熟悉 Go 和 Redis").Score != original.Score {
		t.Fatal("freshness changed capability priority")
	}
	j.Locations = []string{"杭州市"}
	acceptable := s.Screen(j, "熟悉 Go 和 Redis")
	if acceptable.Score != original.Score-4 || acceptable.ExcludedReason != "" {
		t.Fatal(acceptable, original)
	}
	j.Locations = []string{"未知城市"}
	unknown := s.Screen(j, "熟悉 Go 和 Redis")
	if unknown.ExcludedReason != "" {
		t.Fatal("city preference became hard gate", unknown)
	}
	v := s.Screen(localJob(), "岗位要求尚未公布")
	if v.Tier != "UNCERTAIN" || v.Score != 50 {
		t.Fatal("title/preferences concealed missing evidence", v)
	}
}
func TestLocalQualificationConservativeGates(t *testing.T) {
	s := localFixture(t, localProfile(), nil)
	for _, tc := range []struct {
		text     string
		excluded bool
	}{
		{"degree: PHD\ngraduation: 2027", true},
		{"本科及以上\n2027届毕业生", false},
		{"硕士及以上优先", false},
		{"2026、2027届毕业生", false},
		{"2026至2028年毕业生", false},
		{"2027校招", false},
		{"2026年9月至2027年8月毕业生", false},
		{"degree: PHD\ndegree: BACHELOR", false},
		{"2026届毕业生\n2027届毕业生", false},
		{"graduation: 2026", true},
	} {
		v := s.Screen(localJob(), tc.text)
		if (v.ExcludedReason != "") != tc.excluded {
			t.Errorf("%q: %+v", tc.text, v)
		}
	}
	p := localProfile()
	p.GraduationYear = 0
	p.GraduationFrom = 2026
	p.GraduationTo = 2027
	v := localFixture(t, p, nil).Screen(localJob(), "graduation: 2027")
	if v.ExcludedReason != "" || !strings.Contains(strings.Join(v.Warnings, ""), "部分") {
		t.Fatal(v)
	}
}
func TestLocalCacheBoundedConcurrentAndPersonalResultsFresh(t *testing.T) {
	cache := newLocalParseCache(2, 1<<20)
	cache.get("熟悉 Go")
	cache.get("熟悉 Redis")
	cache.get("熟悉 MySQL")
	if cache.lru.Len() != 2 || cache.bytes > cache.maxBytes {
		t.Fatal(cache)
	}
	tiny := newLocalParseCache(2, 1)
	tiny.get("熟悉 Go")
	if tiny.lru.Len() != 0 {
		t.Fatal("oversized item cached")
	}
	s := localFixture(t, localProfile(), nil)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 30; n++ {
				cache.get("熟悉 Go")
				s.Screen(localJob(), "熟悉 Go")
			}
		}()
	}
	wg.Wait()
	before := s.Screen(localJob(), "熟悉 Go")
	p := localProfile()
	p.Skills = []string{"Python"}
	after := localFixture(t, p, nil).Screen(localJob(), "熟悉 Go")
	if before.Checks[0].Result == after.Checks[0].Result {
		t.Fatal("candidate evidence was cached across profiles")
	}
	before.Checks[0].Terms[0] = "corrupted"
	if s.Screen(localJob(), "熟悉 Go").Checks[0].Terms[0] != "Go" {
		t.Fatal("public result mutated cached parser")
	}
	if s.Screen(localJob(), "熟悉 Python").Checks[0].Result != "NO_EVIDENCE" {
		t.Fatal("changed JD reused old parse")
	}
}

func TestLocalCityPreferenceUsesSameCanonicalChoicesAsRadar(t *testing.T) {
	p := localProfile()
	p.PreferredCities = []string{"Hangzhou / Shanghai"}
	p.AcceptableCities = []string{"中国 北京市 海淀区"}
	s := localFixture(t, p, nil)
	job := localJob()
	job.Locations = []string{"杭州市"}
	plain := s.Screen(job, "熟悉 Go")
	job.Locations = []string{"浙江省-杭州市-余杭区", "Hangzhou", "上海市"}
	if got := s.Screen(job, "熟悉 Go"); got.Score != plain.Score {
		t.Fatal("alias/duplicate city changed preference score", plain, got)
	}
	job.Locations = []string{"中国 北京市"}
	if got := s.Screen(job, "熟悉 Go"); got.Score != plain.Score-4 {
		t.Fatal("acceptable city failed to match", plain, got)
	}
	job.Locations = []string{"浙江省", "全国", "桐庐县"}
	if got := s.Screen(job, "熟悉 Go"); got.Score != plain.Score-12 {
		t.Fatal("province or unknown place inferred as preferred city", plain, got)
	}
}
