package resume

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeModel struct {
	called bool
	system string
	input  string
	output string
}

type fakeJSONModel struct {
	fakeModel
	structured bool
}

func (m *fakeJSONModel) CompleteJSON(ctx context.Context, messages any, tools any) (json.RawMessage, error) {
	m.structured = true
	return m.fakeModel.Complete(ctx, messages, tools)
}

func (m *fakeModel) Complete(_ context.Context, messages any, _ any) (json.RawMessage, error) {
	m.called = true
	rows := messages.([]map[string]string)
	m.system = rows[0]["content"]
	m.input = rows[1]["content"]
	return json.Marshal(map[string]string{"role": "assistant", "content": m.output})
}

func TestAnalyzeSendsOnlyReviewedTextAndBindsExcerpts(t *testing.T) {
	text := "2027届本科。项目：任务队列。使用 Go 和 Redis 实现失败重试。"
	m := &fakeModel{output: `{"suggestions":[{"field":"graduation_year","value":"2027","excerpt":"2027届"},{"field":"technical_skills","value":"Go","excerpt":"Go"}],"projects":[{"name":"任务队列","excerpt":"任务队列","facts":[{"kind":"IMPLEMENTED","claim":"实现失败重试","excerpt":"实现失败重试"}]}]}`}
	draft, err := Analyze(context.Background(), m, text)
	if err != nil || !m.called || m.input != text || len(draft.Projects) != 1 {
		t.Fatalf("draft=%+v input=%q err=%v", draft, m.input, err)
	}
	m.output = strings.Replace(m.output, `"excerpt":"实现失败重试"`, `"excerpt":"支持百万用户"`, 1)
	draft, err = Analyze(context.Background(), m, text)
	if err != nil || len(draft.Suggestions) != 2 || len(draft.Projects[0].Facts) != 0 || len(draft.Warnings) != 1 || draft.Warnings[0].Reason != "EXCERPT_NOT_EXACT" {
		t.Fatalf("supported items lost or unsupported fact accepted: %+v %v", draft, err)
	}
}

func TestSensitiveTextBlockedBeforeModelCall(t *testing.T) {
	for _, s := range []string{"张三 13812345678 熟悉 Go", "手机 +86 138 1234 5678 熟悉 Go", "138-1234-5678 熟悉 Go", "姓名：张三\n熟悉 Go", "姓名 张三 熟悉 Go", "Full Name: Alice Zhang 熟悉 Go", "邮箱 x@example.com 熟悉 Go", "github.com/someone/repo 熟悉 Go"} {
		m := &fakeModel{}
		if _, err := Analyze(context.Background(), m, s); !errors.Is(err, ErrSensitive) || m.called {
			t.Fatalf("text=%q err=%v called=%v", s, err, m.called)
		}
	}
}

func TestAnalyzeRequestsStructuredModeWhenAvailable(t *testing.T) {
	m := &fakeJSONModel{fakeModel: fakeModel{output: `{"suggestions":[],"projects":[]}`}}
	if _, err := Analyze(context.Background(), m, "熟悉 Go"); err != nil || !m.structured {
		t.Fatalf("structured mode missing: %v", err)
	}
}

func TestAnalyzeDoesNotTurnGoImplementationDetailsIntoSkills(t *testing.T) {
	text := "熟悉 Go、Redis、Go 并发编程。任务队列使用 goroutine、sync.Mutex、Go channel 和 sync/atomic 实现并发控制。"
	m := &fakeModel{output: `{"suggestions":[{"field":"target_languages","value":"Go","excerpt":"Go"},{"field":"technical_skills","value":"Redis","excerpt":"Redis"},{"field":"technical_skills","value":"Go 并发编程","excerpt":"Go 并发编程"},{"field":"technical_skills","value":"goroutine","excerpt":"goroutine"},{"field":"technical_skills","value":"sync","excerpt":"sync"},{"field":"technical_skills","value":"sync.Mutex","excerpt":"sync.Mutex"},{"field":"technical_skills","value":"Go channel","excerpt":"Go channel"},{"field":"technical_skills","value":"sync/atomic","excerpt":"sync/atomic"},{"field":"technical_skills","value":"goroutine、sync.Mutex","excerpt":"goroutine、sync.Mutex"},{"field":"target_languages","value":"sync","excerpt":"sync"}],"projects":[{"name":"任务队列","excerpt":"任务队列","facts":[{"kind":"IMPLEMENTED","claim":"使用 goroutine 和 sync.Mutex 实现并发控制","excerpt":"goroutine、sync.Mutex"}]}]}`}
	draft, err := Analyze(context.Background(), m, text)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.system, "Do not list language syntax") || len(draft.Suggestions) != 3 {
		t.Fatalf("unexpected skill draft: %+v", draft.Suggestions)
	}
	for i, want := range []string{"Go", "Redis", "Go 并发编程"} {
		if draft.Suggestions[i].Value != want {
			t.Fatalf("skill %d = %q, want %q", i, draft.Suggestions[i].Value, want)
		}
	}
	if len(draft.Projects) != 1 || len(draft.Projects[0].Facts) != 1 || !strings.Contains(draft.Projects[0].Facts[0].Claim, "sync.Mutex") {
		t.Fatalf("implementation evidence lost: %+v", draft.Projects)
	}
}
