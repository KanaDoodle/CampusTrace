package agent

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type TokenBudget struct {
	Limit            int `json:"limit"`
	Charged          int `json:"charged"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	UnreportedCalls  int `json:"unreported_calls"`
}

func budgetLimit(n int) int {
	if n <= 0 {
		return 48000
	}
	return n
}

// Bytes are a conservative admission estimate, NOT measured or billed tokens.
func requestEstimate(messages []Message, defs []Definition) int {
	b, e := json.Marshal(messages)
	if e == nil {
		return len(b) + len(d.JSON(defs)) + 512
	}
	// Invalid model-supplied RawMessage arguments are diagnostic data, not a
	// reason for the budget estimator to panic or bypass runtime validation.
	n := 512 + len(d.JSON(defs))
	for _, m := range messages {
		n += len(m.Content)*2 + 128
		for _, c := range m.Calls {
			n += len(c.Args)*2 + len(c.Name) + 128
		}
	}
	return n
}
func (b *TokenBudget) admit(input, output int) bool { return b.Charged+input+output <= b.Limit }
func (b *TokenBudget) charge(u analysis.Usage, input, output int) {
	if u.Known {
		b.PromptTokens += u.PromptTokens
		b.CompletionTokens += u.CompletionTokens
		b.Charged += u.PromptTokens + u.CompletionTokens
	} else {
		b.UnreportedCalls++
		b.Charged += input + output
	}
}
func capModel(model Model, output int) Model {
	if m, ok := model.(LiveModel); ok {
		client := *m.Client
		client.OutputTokenLimit = output
		return LiveModel{&client}
	}
	return model
}

type Router struct {
	Primary     Model
	Complex     Model
	LocalLookup bool
}

func (r Router) Choose(q string) (Model, string) {
	// Only deterministic, supported read routes qualify for the local bypass.
	if r.LocalLookup && !Has(q, "记住:", "记住：", "创建", "修改", "比较", "对比", "为什么", "分析", "准备", "复习") {
		reply, _ := (DemoModel{}).Next(context.Background(), []Message{{Role: "user", Content: q}}, nil)
		if len(reply.Calls) > 0 {
			read := true
			for _, c := range reply.Calls {
				read = read && !IsWrite(c.Name)
			}
			if read {
				return DemoModel{}, "LOCAL_LOOKUP"
			}
		}
	}
	if r.Complex != nil && Has(q, "比较", "对比", "准备", "复习", "原因", "为什么", "分析", "取舍") {
		return r.Complex, "COMPLEX"
	}
	return r.Primary, "PRIMARY"
}

var ErrContextBudget = errors.New("context budget exceeded")
