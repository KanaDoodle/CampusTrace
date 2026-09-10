package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
	"strings"
)

type LiveModel struct{ Client *analysis.ChatClient }

func (m LiveModel) Next(ctx context.Context, messages []Message, definitions []Definition) (Reply, error) {
	ms := []map[string]any{}
	for _, v := range messages {
		x := map[string]any{"role": v.Role, "content": v.Content}
		if v.ToolCallID != "" {
			x["tool_call_id"] = v.ToolCallID
		}
		if len(v.Calls) > 0 {
			calls := []any{}
			for _, c := range v.Calls {
				calls = append(calls, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(c.Args)}})
			}
			x["tool_calls"] = calls
		}
		ms = append(ms, x)
	}
	defs := []any{}
	for _, v := range definitions {
		defs = append(defs, map[string]any{"type": "function", "function": v})
	}
	raw, err := m.Client.Complete(ctx, ms, defs)
	if err != nil {
		return Reply{}, err
	}
	var v struct {
		Content string `json:"content"`
		Calls   []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		return Reply{}, err
	}
	reply := Reply{Text: v.Content}
	for _, c := range v.Calls {
		reply.Calls = append(reply.Calls, Call{ID: c.ID, Name: c.Function.Name, Args: json.RawMessage(c.Function.Arguments)})
	}
	return reply, nil
}

// DemoModel is a deterministic offline router, not a live-model accuracy claim.
type DemoModel struct{}

func (DemoModel) Next(ctx context.Context, msgs []Message, defs []Definition) (Reply, error) {
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	last := msgs[len(msgs)-1]
	if last.Role == "tool" {
		return Reply{Text: "已查询本次业务数据。请查看下方 grounded_observations 中的原始证据与记录；申请写操作需要单独确认。此回复来自 deterministic demo model。"}, nil
	}
	q := last.Content
	id := regexp.MustCompile(`\b[a-f0-9]{32}\b`).FindString(q)
	calls := []Call{}
	add := func(name string, args any) {
		calls = append(calls, Call{ID: fmt.Sprintf("call-%d", len(calls)), Name: name, Args: []byte(d.JSON(args))})
	}
	switch {
	case Has(q, "投过", "applications", "申请记录"):
		add("list_applications", struct{}{})
	case Has(q, "项目", "project"):
		add("get_project_facts", struct{}{})
	case Has(q, "薄弱", "weak"):
		add("get_weak_topics", struct{}{})
	case id != "" && Has(q, "创建申请", "create application"):
		add("create_application", map[string]string{"job_id": id})
	case id != "" && Has(q, "准备", "prepare"):
		add("get_preparation_context", map[string]string{"job_id": id})
	case id != "":
		add("get_job", map[string]string{"job_id": id})
		add("get_job_evidence", map[string]string{"job_id": id})
		add("get_job_eligibility", map[string]string{"job_id": id})
	case Has(q, "知识", "redis", "复习", "knowledge"):
		add("search_knowledge", map[string]string{"query": q})
	default:
		query := strings.TrimSpace(q)
		if Has(q, "岗位", "jobs") {
			query = "Go"
		}
		add("search_jobs", map[string]string{"query": query})
	}
	return Reply{Calls: calls}, nil
}
