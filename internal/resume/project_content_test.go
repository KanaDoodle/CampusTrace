package resume

import (
	"strings"
	"testing"
)

func TestResumeKeepsCompleteBulletAndRepairsFragmentedAccomplishment(t *testing.T) {
	description := "个人招聘跟踪工具，支持职位收集与投递记录。"
	bullet := "使用 Redis Streams 实现异步任务处理，\n通过消费者组和失败重试提高可靠性。"
	text := "项目：任务队列\n" + description + "\n• " + bullet
	draft, err := analyzeDraft(t, text, Draft{Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Description: description, Bullets: []string{bullet}, Facts: []Fact{
		{Kind: "IMPLEMENTED", Claim: "使用 Redis Streams", Excerpt: "Redis Streams"},
		{Kind: "IMPLEMENTED", Claim: "使用消费者组", Excerpt: "消费者组"},
		{Kind: "IMPLEMENTED", Claim: "实现失败重试", Excerpt: "失败重试"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	project := draft.Projects[0]
	if project.Description != description || len(project.Bullets) != 1 || project.Bullets[0] != bullet || len(project.Facts) != 1 || project.Facts[0].Claim != bullet || project.Facts[0].Excerpt != bullet {
		t.Fatalf("lost the accomplishment chain: %+v", project)
	}
	if err := Validate(draft, text); err != nil {
		t.Fatal(err)
	}
}

func TestResumeNeverExpandsImplementedEvidenceAcrossPlansOrLimitations(t *testing.T) {
	for _, bullet := range []string{"实现 Go 服务；计划添加 Kafka。", "实现 Go 服务；尚未部署生产环境。", "Built a Go service, without production traffic."} {
		quote := "Go"
		draft, err := analyzeDraft(t, "任务队列\n"+bullet, Draft{Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Bullets: []string{bullet}, Facts: []Fact{{Kind: "IMPLEMENTED", Claim: "使用 Go", Excerpt: quote}}}}})
		if err != nil || draft.Projects[0].Facts[0].Claim != "使用 Go" || draft.Projects[0].Facts[0].Excerpt != quote {
			t.Fatalf("expanded across an unimplemented scope: %+v %v", draft, err)
		}
	}
	bullet := "实现 Go 服务；下一步添加 Kafka。"
	draft, err := analyzeDraft(t, "任务队列\n"+bullet, Draft{Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Bullets: []string{bullet}, Facts: []Fact{{Kind: "IMPLEMENTED", Claim: "实现 Go 服务", Excerpt: "实现 Go 服务"}, {Kind: "PLANNED", Claim: "添加 Kafka", Excerpt: "下一步添加 Kafka"}}}}})
	if err != nil || len(draft.Projects[0].Facts) != 2 || draft.Projects[0].Facts[1].Kind != "PLANNED" || draft.Projects[0].Facts[0].Excerpt != "实现 Go 服务" {
		t.Fatalf("merged different fact types: %+v %v", draft, err)
	}
}

func TestResumeProjectContentMustBeOriginalAndBelongToItsProject(t *testing.T) {
	text := "项目：队列\n使用 Redis 执行异步任务。\n项目：搜索\n使用 Elasticsearch 实现搜索。"
	draft, err := analyzeDraft(t, text, Draft{Projects: []Project{
		{Name: "队列", Excerpt: "项目：队列", Description: "模型改写的简介", Bullets: []string{"使用 Elasticsearch 实现搜索。", "使用 Redis 执行异步任务。"}, Facts: []Fact{{Kind: "IMPLEMENTED", Claim: "执行异步任务", Excerpt: "执行异步任务"}}},
		{Name: "搜索", Excerpt: "项目：搜索", Bullets: []string{"使用 Elasticsearch 实现搜索。"}, Facts: []Fact{}},
	}})
	if err != nil || len(draft.Warnings) != 2 || draft.Warnings[0].Scope != "PROJECT_DESCRIPTION" || draft.Warnings[1].Reason != "EXCERPT_OUTSIDE_PROJECT" || draft.Projects[0].Description != "" || len(draft.Projects[0].Bullets) != 1 || strings.Contains(draft.Projects[0].Facts[0].Claim, "Elasticsearch") {
		t.Fatalf("project context accepted without provenance: %+v %v", draft, err)
	}
}

func TestResumeRestoresWrappedFullBulletWithoutSplittingAtTheOldQuoteLimit(t *testing.T) {
	bullet := strings.Repeat("处理异步任务，", 40) + "\n使用 Go 实现失败重试。"
	draft, err := analyzeDraft(t, "任务队列\n"+bullet, Draft{Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Bullets: []string{strings.ReplaceAll(bullet, "\n", "")}, Facts: []Fact{{Kind: "IMPLEMENTED", Claim: "使用 Go 实现失败重试", Excerpt: "使用 Go 实现失败重试。"}}}}})
	if err != nil || len(draft.Projects[0].Facts) != 1 || draft.Projects[0].Facts[0].Claim != bullet || draft.Projects[0].Bullets[0] != bullet || draft.NormalizedExcerpts != 1 {
		t.Fatalf("full source bullet was truncated or split: %+v %v", draft, err)
	}
}

func TestResumeKeepsAmbiguousRepeatedClausesSeparate(t *testing.T) {
	facts := []Fact{{Kind: "IMPLEMENTED", Claim: "执行任务", Excerpt: "执行任务"}}
	got := completeAccomplishments(facts, []string{"使用 Go 执行任务。", "使用 Java 执行任务。"})
	if len(got) != 1 || got[0].Claim != facts[0].Claim || got[0].Excerpt != facts[0].Excerpt {
		t.Fatal("an ambiguous clause selected a bullet", got)
	}
}

func TestResumeUsesSourceBulletBoundaryWhenTheModelAlsoSplitsBullets(t *testing.T) {
	first := "使用 Redis Streams 实现异步任务处理，\n通过消费者组和失败重试提高可靠性。"
	second := "使用任务唯一键实现幂等处理，避免重复执行。"
	text := "任务队列\n• " + first + "\n• " + second
	draft, err := analyzeDraft(t, text, Draft{Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Bullets: []string{"使用 Redis Streams", "消费者组", "失败重试", second}, Facts: []Fact{
		{Kind: "IMPLEMENTED", Claim: "使用 Redis Streams", Excerpt: "Redis Streams"},
		{Kind: "IMPLEMENTED", Claim: "使用消费者组", Excerpt: "消费者组"},
		{Kind: "IMPLEMENTED", Claim: "实现失败重试", Excerpt: "失败重试"},
		{Kind: "IMPLEMENTED", Claim: "实现幂等处理", Excerpt: "任务唯一键实现幂等处理"},
	}}}})
	if err != nil || len(draft.Projects[0].Bullets) != 2 || len(draft.Projects[0].Facts) != 2 || draft.Projects[0].Bullets[0] != first || draft.Projects[0].Facts[0].Claim != first || draft.Projects[0].Facts[1].Claim != second {
		t.Fatalf("model fragments overrode the original bullet boundaries: %+v %v", draft, err)
	}
}
