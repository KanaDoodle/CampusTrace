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
	if _, err := Analyze(context.Background(), m, text); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported excerpt accepted: %v", err)
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
