package integration

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

type resumeReviewModel struct{ calls atomic.Int32 }

func (m *resumeReviewModel) Complete(_ context.Context, messages, _ any) (json.RawMessage, error) {
	m.calls.Add(1)
	var input []map[string]string
	if err := json.Unmarshal([]byte(d.JSON(messages)), &input); err != nil {
		return nil, err
	}
	draft := map[string]any{"suggestions": []resume.Suggestion{}, "projects": []resume.Project{{Name: "任务队列", Excerpt: "任务队列", Facts: []resume.Fact{{Kind: "IMPLEMENTED", Claim: "实现百万并发", Excerpt: "百万并发"}}}}}
	if strings.Contains(input[1]["content"], "Go") {
		draft["suggestions"] = []resume.Suggestion{{Field: "target_languages", Value: "Go", Excerpt: "Go"}}
		draft["projects"] = []resume.Project{{Name: "任务队列", Excerpt: "任务队列", Facts: []resume.Fact{{Kind: "IMPLEMENTED", Claim: "使用 Go 实现失败重试", Excerpt: "使用Go，实现失败重试。"}, {Kind: "IMPLEMENTED", Claim: "实现百万并发", Excerpt: "百万并发"}}}}
	}
	return json.Marshal(map[string]string{"content": d.JSON(draft)})
}

func TestResumeReviewRetainsUsableDraftWithoutSavingOrResendingPrivateInput(t *testing.T) {
	ctx, store, queue, owner, _ := setup(t)
	must(t, store.SaveProfile(ctx, owner, d.Profile{GraduationYear: 2027, Languages: []string{"Java"}}))
	before, err := store.Profile(ctx, owner)
	must(t, err)
	authn := auth.Service{Store: store, Secret: []byte("synthetic-resume-review-secret-32")}
	token, err := authn.Token(owner)
	must(t, err)
	model := &resumeReviewModel{}
	handler := (&transport.API{Store: store, Queue: queue, Auth: authn, Metrics: observability.New(), ResumeModel: model, ResumeModelName: "fixture"}).Handler()
	rec := matchingRequest(handler, token, "/api/profile/resume/draft", "POST", map[string]any{"text": "项目：任务队列。使用 Go，\n实现失败重试。"})
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("review %d %s", rec.Code, rec.Body.String())
	}
	var draft resume.Draft
	must(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	if len(draft.Suggestions) != 1 || len(draft.Projects) != 1 || len(draft.Projects[0].Facts) != 1 || draft.Projects[0].Facts[0].Excerpt != "使用 Go，\n实现失败重试。" || len(draft.Warnings) != 1 || draft.Warnings[0].Reason != "EXCERPT_NOT_EXACT" || draft.NormalizedExcerpts != 1 {
		t.Fatalf("draft %+v", draft)
	}
	if strings.Contains(rec.Body.String(), "百万并发") || model.calls.Load() != 1 {
		t.Fatal("rejected text returned or review retried the model")
	}
	after, err := store.Profile(ctx, owner)
	must(t, err)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("draft changed saved profile")
	}
	for _, table := range []string{"projects", "project_facts"} {
		rows, err := store.Owned(ctx, table, owner)
		must(t, err)
		if len(rows) != 0 {
			t.Fatal("draft automatically persisted", table)
		}
	}
	rec = matchingRequest(handler, token, "/api/profile/resume/draft", "POST", map[string]any{"text": "项目：任务队列。计划添加监控。"})
	var failure struct {
		Code       string            `json:"code"`
		RequestID  string            `json:"request_id"`
		Diagnostic resume.Diagnostic `json:"diagnostic"`
	}
	must(t, json.Unmarshal(rec.Body.Bytes(), &failure))
	if rec.Code != 502 || failure.Code != "RESUME_DRAFT_UNVERIFIABLE" || len(failure.RequestID) != 32 || failure.Diagnostic.Reason != "EXCERPT_NOT_EXACT" || failure.Diagnostic.Scope != "FACT" || failure.Diagnostic.ItemIndex != 1 || model.calls.Load() != 2 {
		t.Fatalf("all invalid %d %s calls=%d", rec.Code, rec.Body.String(), model.calls.Load())
	}
	rec = matchingRequest(handler, token, "/api/profile/resume/draft", "POST", map[string]any{"text": "联系 test-private@example.com，任务队列。"})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "RESUME_PII_DETECTED") || model.calls.Load() != 2 {
		t.Fatal("private input reached model", rec.Code, rec.Body.String(), model.calls.Load())
	}
}
