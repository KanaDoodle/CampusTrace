package resume

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func analyzeDraft(t *testing.T, text string, draft Draft) (Draft, error) {
	t.Helper()
	wire := struct {
		Suggestions []Suggestion `json:"suggestions"`
		Projects    []Project    `json:"projects"`
	}{draft.Suggestions, draft.Projects}
	if wire.Suggestions == nil {
		wire.Suggestions = []Suggestion{}
	}
	if wire.Projects == nil {
		wire.Projects = []Project{}
	}
	b, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return Analyze(context.Background(), &fakeModel{output: string(b)}, text)
}

func TestResumeWhitespaceRepairReturnsOriginalContiguousSlice(t *testing.T) {
	text := "项目：任务队列\n使用 Go\u00a0 和 Redis，\r\n实现失败重试。"
	draft, err := analyzeDraft(t, text, Draft{Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Facts: []Fact{{Kind: "IMPLEMENTED", Claim: "使用 Go 和 Redis 实现失败重试", Excerpt: "使用Go和Redis，实现失败重试。"}}}}})
	if err != nil || len(draft.Projects) != 1 || len(draft.Projects[0].Facts) != 1 {
		t.Fatalf("%+v %v", draft, err)
	}
	actual := draft.Projects[0].Facts[0].Excerpt
	if actual != "使用 Go\u00a0 和 Redis，\r\n实现失败重试。" || !strings.Contains(text, actual) || draft.NormalizedExcerpts != 1 || len(draft.Warnings) != 0 {
		t.Fatalf("reconstructed quote %q %+v", actual, draft)
	}
	if err := Validate(draft, text); err != nil {
		t.Fatal(err)
	}
}

func TestResumeWhitespaceRepairDoesNotAlterMeaningOrJoinPassages(t *testing.T) {
	text := "项目：任务队列\n使用 Go 和 Redis，实现失败重试。\n未实现分布式锁。\n实现幂等。"
	for _, quote := range []string{"使用 go 和 Redis，实现失败重试。", "使用 Go 和 Redis,实现失败重试。", "使用Go和Redis，实现失败重试。实现幂等。", "使用Go和Redis，实现百万并发。", "实现失败重试…实现幂等。"} {
		t.Run(quote, func(t *testing.T) {
			_, err := analyzeDraft(t, text, Draft{Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Facts: []Fact{{Kind: "IMPLEMENTED", Claim: "实现失败重试", Excerpt: quote}}}}})
			var issue *ValidationError
			if !errors.Is(err, ErrInvalid) || !errors.As(err, &issue) || issue.Reason != "EXCERPT_NOT_EXACT" || issue.Scope != "FACT" || issue.ProjectIndex != 1 || issue.ItemIndex != 1 {
				t.Fatalf("accepted changed quote or lost position: %v", err)
			}
		})
	}
}

func TestResumePartialDraftRetainsValidFactsAndReportsExcludedPositions(t *testing.T) {
	text := "2027届。项目：任务队列。实现失败重试；计划添加监控。"
	draft, err := analyzeDraft(t, text, Draft{Suggestions: []Suggestion{{Field: "graduation_year", Value: "2027", Excerpt: "2027届"}, {Field: "degree", Value: "本科", Excerpt: "2027届"}}, Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Facts: []Fact{{Kind: "IMPLEMENTED", Claim: "实现失败重试", Excerpt: "实现失败重试"}, {Kind: "IMPLEMENTED", Claim: "实现百万并发", Excerpt: "百万并发"}, {Kind: "PLANNED", Claim: "添加监控", Excerpt: "计划添加监控"}}}}})
	if err != nil || len(draft.Suggestions) != 1 || len(draft.Projects[0].Facts) != 2 || len(draft.Warnings) != 2 {
		t.Fatalf("partial %+v %v", draft, err)
	}
	if draft.Warnings[0].Reason != "VALUE_FORMAT" || draft.Warnings[0].Scope != "SUGGESTION" || draft.Warnings[0].ItemIndex != 2 || draft.Warnings[1].Reason != "EXCERPT_NOT_EXACT" || draft.Warnings[1].ItemIndex != 2 {
		t.Fatal(draft.Warnings)
	}
	if draft.Projects[0].Facts[1].Kind != "PLANNED" {
		t.Fatal("plan changed to implemented")
	}
	if err := Validate(draft, text); err != nil {
		t.Fatal(err)
	}
}

func TestResumeUnboundOrSensitiveProjectDoesNotKeepItsFacts(t *testing.T) {
	for _, project := range []Project{{Name: "未出现的项目", Excerpt: "未出现的项目"}, {Name: "姓名：张三", Excerpt: "任务队列"}} {
		project.Facts = []Fact{{Kind: "IMPLEMENTED", Claim: "实现失败重试", Excerpt: "实现失败重试"}}
		draft, err := analyzeDraft(t, "2027届。任务队列实现失败重试。", Draft{Suggestions: []Suggestion{{Field: "graduation_year", Value: "2027", Excerpt: "2027届"}}, Projects: []Project{project}})
		if err != nil || len(draft.Projects) != 0 || len(draft.Suggestions) != 1 || len(draft.Warnings) != 1 || draft.Warnings[0].Scope != "PROJECT" {
			t.Fatalf("unbound project accepted %+v %v", draft, err)
		}
		b, _ := json.Marshal(draft)
		if strings.Contains(string(b), "张三") {
			t.Fatal("excluded sensitive content returned")
		}
	}
}

func TestResumeLongExcerptsAndAmbiguousWhitespaceAreNotSilentlyTruncated(t *testing.T) {
	for _, tc := range []struct{ text, quote, reason string }{{"2027届。" + strings.Repeat("后", 201), strings.Repeat("后", 201), "EXCERPT_LENGTH"}, {"2027届。使用Go和Redis。\n使用 Go 和 Redis。", "使用Go 和Redis。", "EXCERPT_AMBIGUOUS"}} {
		draft, err := analyzeDraft(t, tc.text, Draft{Suggestions: []Suggestion{{Field: "graduation_year", Value: "2027", Excerpt: "2027届"}, {Field: "technical_skills", Value: "Redis", Excerpt: tc.quote}}})
		if err != nil || len(draft.Suggestions) != 1 || len(draft.Warnings) != 1 || draft.Warnings[0].Reason != tc.reason {
			t.Fatalf("%+v %v", draft, err)
		}
	}
}

func TestResumeResponseDiagnosticsAndSingleFence(t *testing.T) {
	for _, tc := range []struct{ output, reason string }{{"说明文字 {\"suggestions\":[],\"projects\":[]}", "RESPONSE_JSON"}, {`{"suggestions":null,"projects":[]}`, "RESPONSE_SCHEMA"}, {`{"suggestions":[],"projects":[],"warnings":[{"validation_reason":"fake"}]}`, "RESPONSE_SCHEMA"}, {`{"suggestions":[]}`, "RESPONSE_SCHEMA"}, {`{"suggestions":[],"projects":[]`, "RESPONSE_JSON"}} {
		_, err := Analyze(context.Background(), &fakeModel{output: tc.output}, "任务队列")
		var issue *ValidationError
		if !errors.As(err, &issue) || issue.Reason != tc.reason {
			t.Fatalf("reason %s: %v", tc.reason, err)
		}
	}
	draft, err := Analyze(context.Background(), &fakeModel{output: "```json\n{\"suggestions\":[{\"field\":\"target_languages\",\"value\":\"Go\",\"excerpt\":\"Go\"}],\"projects\":[]}\n```"}, "熟悉 Go")
	if err != nil || len(draft.Suggestions) != 1 {
		t.Fatalf("fence %+v %v", draft, err)
	}
}

func TestResumeLimitsRemainStrict(t *testing.T) {
	_, err := analyzeDraft(t, "Go", Draft{Suggestions: make([]Suggestion, 61)})
	var issue *ValidationError
	if !errors.As(err, &issue) || issue.Reason != "DRAFT_LIMIT" {
		t.Fatal(err)
	}
	_, err = analyzeDraft(t, "任务队列", Draft{Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Facts: make([]Fact, 21)}}})
	if !errors.As(err, &issue) || issue.Reason != "PROJECT_FACTS_LIMIT" || issue.ProjectIndex != 1 {
		t.Fatal(err)
	}
}

func TestResumeFilteredPrimitiveDoesNotHideAllInvalidFacts(t *testing.T) {
	_, err := analyzeDraft(t, "任务队列使用 goroutine。", Draft{Suggestions: []Suggestion{{Field: "technical_skills", Value: "goroutine", Excerpt: "goroutine"}}, Projects: []Project{{Name: "任务队列", Excerpt: "任务队列", Facts: []Fact{{Kind: "IMPLEMENTED", Claim: "实现百万并发", Excerpt: "百万并发"}}}}})
	var issue *ValidationError
	if !errors.As(err, &issue) || issue.Reason != "EXCERPT_NOT_EXACT" || issue.Scope != "FACT" {
		t.Fatalf("empty draft hid invalid facts: %v", err)
	}
}
