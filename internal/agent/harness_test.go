package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type retryTools struct {
	n    int
	fail error
	defs []Definition
}

func (t *retryTools) Definitions() []Definition { return t.defs }
func (t *retryTools) Execute(context.Context, string, string, json.RawMessage) (any, error) {
	t.n++
	if t.n == 1 && t.fail != nil {
		return nil, t.fail
	}
	return map[string]any{"text": "test", "count": 13800138000}, nil
}

type temporaryRead struct{}

func (temporaryRead) Error() string   { return "temporary" }
func (temporaryRead) Timeout() bool   { return false }
func (temporaryRead) Temporary() bool { return true }

type permanentRead struct{}

func (permanentRead) Error() string   { return "permanent DNS failure" }
func (permanentRead) Timeout() bool   { return false }
func (permanentRead) Temporary() bool { return false }

func TestHarnessReviewedPlansAndBoundaries(t *testing.T) {
	for _, s := range Skills() {
		in := SkillInput{SkillID: s.ID, RequestKey: "unique-key-123"}
		switch s.Input {
		case "company":
			in.Company = "腾讯"
		case "topic":
			in.Topic = "Redis"
		case "job_id":
			in.JobID = strings.Repeat("a", 32)
		}
		if e := in.Validate(); e != nil {
			t.Fatal(e)
		}
		calls := in.Plan()
		if len(calls) > s.MaxCalls || len(calls) == 0 {
			t.Fatal(calls)
		}
		for _, c := range calls {
			if Validate(c.Name, c.Args) != nil || IsWrite(c.Name) {
				t.Fatal(c)
			}
		}
		in.JobID = "bad-owner-override"
		if in.Validate() == nil {
			t.Fatal("unexpected unused input accepted")
		}
	}
	for _, raw := range []string{`{"connector_id":"` + strings.Repeat("a", 32) + `","uri":"file:///private"}`, `{"connector_id":"bad","resource_id":"` + strings.Repeat("b", 64) + `"}`, `{"user_id":"other"}`} {
		if Validate("read_mcp_resource", []byte(raw)) == nil {
			t.Fatal(raw)
		}
	}
}
func TestContextSelectionAndAuthority(t *testing.T) {
	rows := []p.AgentMemory{{MemoryInput: p.MemoryInput{ID: "other", Kind: "DECISION", Scope: "美团", Content: "只投一份"}}, {MemoryInput: p.MemoryInput{ID: "tencent", Kind: "DECISION", Scope: "腾讯", Content: "优先后端"}}, {MemoryInput: p.MemoryInput{ID: "fix", Kind: "CORRECTION", Content: "不把计划写为已实现"}}}
	got := relevantMemories("腾讯怎么选", rows)
	if len(got) != 2 || got[0].ID != "tencent" {
		t.Fatal(got)
	}
	defs := selectDefinitions("查看练习记录", "", (&Tools{}).Definitions())
	if len(defs) != 2 {
		t.Fatal(defs)
	}
	tools := &retryTools{defs: (&Tools{}).Definitions()}
	model := &Scripted{Replies: []Reply{{Calls: []Call{{ID: "c", Name: "create_application", Args: []byte(`{"job_id":"` + strings.Repeat("a", 32) + `","resume_version":"v1"}`)}}}}}
	r := Runtime{Model: model, Tools: tools, SkillID: "daily-review", Deadline: time.Second, ToolTimeout: time.Second, MaxModels: 3, MaxTools: 8}
	out := r.Run(context.Background(), "u", "s", "今天", nil)
	if tools.n != 0 || out.Executed != 0 {
		t.Fatal("skill authority expanded", out)
	}
}
func TestReadRetryAndStructuredFailure(t *testing.T) {
	tools := &retryTools{fail: temporaryRead{}}
	_, n, e := ExecuteRead(context.Background(), tools, "u", Call{Name: "search_jobs", Args: []byte(`{"query":"Go"}`)})
	if e != nil || n != 2 || tools.n != 2 {
		t.Fatal(n, e)
	}
	tools = &retryTools{fail: p.ErrValidation}
	_, n, e = ExecuteRead(context.Background(), tools, "u", Call{Name: "search_jobs"})
	if e == nil || n != 1 {
		t.Fatal(n, e)
	}
	tools = &retryTools{fail: temporaryRead{}}
	_, n, e = ExecuteRead(context.Background(), tools, "u", Call{Name: "create_application"})
	if e == nil || n != 1 {
		t.Fatal("write repeated")
	}
	if f := ClassifyToolFailure(errors.Join(errors.New("wrapped"), p.ErrConflict)); f.Code != "STALE_INPUT" || f.Retryable {
		t.Fatal(f)
	}
	tools = &retryTools{fail: permanentRead{}}
	_, n, e = ExecuteRead(context.Background(), tools, "u", Call{Name: "search_jobs"})
	if e == nil || n != 1 || ClassifyToolFailure(e).Retryable {
		t.Fatal("permanent network error was retried", n, e)
	}
}
func TestObservationRedactionPreservesJSONNumbers(t *testing.T) {
	v, e := safeObservation(map[string]any{"name": "测试姓名", "nested": []any{map[string]string{"text": "手机13800138000 email test@example.com"}}, "number": int64(13800138000)}, "测试姓名")
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(v)
	if e != nil || !json.Valid(b) || strings.Contains(string(b), "test@example.com") || strings.Contains(string(b), "手机13800138000") || strings.Contains(string(b), "测试姓名") {
		t.Fatal(string(b), e)
	}
	if !strings.Contains(string(b), `"number":13800138000`) {
		t.Fatal(string(b))
	}
}
func TestRoutingAndTokenBudgetStopsNextProviderCall(t *testing.T) {
	primary := &Scripted{}
	complex := &Scripted{}
	router := Router{Primary: primary, Complex: complex, LocalLookup: true}
	if m, route := router.Choose("查看求职待办"); route != "LOCAL_LOOKUP" || m == primary {
		t.Fatal(route)
	}
	if m, route := router.Choose("为什么这个岗位排名高"); route != "COMPLEX" || m != complex {
		t.Fatal(route)
	}
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"usage":{"prompt_tokens":19000,"completion_tokens":500},"choices":[{"message":{"content":"","tool_calls":[{"id":"c","function":{"name":"search_jobs","arguments":"{\"query\":\"Go\"}"}}]}}]}`))
	}))
	defer srv.Close()
	client := analysis.NewChat(srv.URL, "synthetic-key", "test", 1)
	r := Runtime{Model: LiveModel{client}, Tools: &retryTools{defs: (&Tools{}).Definitions()}, MaxModels: 4, MaxTools: 8, MaxTotalTokens: 20000, Deadline: time.Second, ToolTimeout: time.Second}
	v := r.Run(context.Background(), "u", "s", "Go岗位", nil)
	if calls != 1 || v.Terminal != "BUDGET_LIMIT" || v.Budget.PromptTokens != 19000 || v.ProviderCalls != 1 || len(v.Facts) != 1 {
		t.Fatalf("calls=%d %+v", calls, v)
	}
	r.MaxTotalTokens = 1
	v = r.Run(context.Background(), "u", "s", "Go岗位", nil)
	if calls != 1 || v.ProviderCalls != 0 {
		t.Fatal("called despite insufficient budget", v)
	}
	r.Router = &Router{Primary: r.Model, LocalLookup: true}
	v = r.Run(context.Background(), "u", "s", "查看求职待办", nil)
	if v.Route != "LOCAL_LOOKUP" || v.ProviderCalls != 0 || calls != 1 {
		t.Fatal(v)
	}
}
