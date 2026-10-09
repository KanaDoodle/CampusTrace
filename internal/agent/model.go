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
	raw, usage, err := m.Client.CompleteWithUsage(ctx, ms, defs)
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
	reply := Reply{Text: v.Content, Usage: usage, Model: m.Client.Model}
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
	ids := regexp.MustCompile(`\b[a-f0-9]{32}\b`).FindAllString(q, 9)
	searchQuery := demoSearchQuery(q)
	id := ""
	if len(ids) > 0 {
		id = ids[0]
	}
	calls := []Call{}
	add := func(name string, args any) {
		calls = append(calls, Call{ID: fmt.Sprintf("call-%d", len(calls)), Name: name, Args: []byte(d.JSON(args))})
	}
	switch {
	case Has(q, "求职待办", "招聘待办", "变化待办"):
		add("get_agent_todos", struct{}{})
	case Has(q, "外部资料", "MCP资料", "MCP 资料"):
		add("list_mcp_resources", struct{}{})
	case Has(q, "完整资料", "完整简历", "核对资料"):
		add("get_candidate_document", struct{}{})
	case strings.HasPrefix(q, "记住：") || strings.HasPrefix(q, "记住:"):
		content := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(q, "记住："), "记住:"))
		add("remember_memory", map[string]string{"kind": "PREFERENCE", "content": content})
	case Has(q, "记忆", "记住了什么"):
		add("search_memories", map[string]string{"query": ""})
	case Has(q, "练习结果", "练习记录"):
		add("get_practice_runs", struct{}{})
	case Has(q, "历史讨论", "讨论记录"):
		add("get_agent_tasks", struct{}{})
	case Has(q, "深度分析进度", "匹配任务", "分析任务") || Has(q, "深度分析") && Has(q, "失败", "进度", "还在", "完成"):
		add("get_match_tasks", struct{}{})
	case len(ids) >= 2 && len(ids) <= 8 && Has(q, "比较", "对比", "哪个", "选择"):
		add("compare_company_jobs", map[string]any{"job_ids": ids})
	case len(ids) == 0 && Has(q, "比较", "对比") && comparisonCompany(q) != "":
		add("compare_company_jobs", map[string]string{"company": comparisonCompany(q)})
	case id != "" && Has(q, "深度", "匹配", "分析结果"):
		add("get_match_result", map[string]string{"job_id": id})
	case Has(q, "今天", "今日", "daily digest", "值得处理"):
		add("get_daily_digest", struct{}{})
	case Has(q, "截止", "closing"):
		days := 7
		if Has(q, "三天", "3天") {
			days = 3
		}
		if Has(q, "14天", "两周") {
			days = 14
		}
		add("get_closing_jobs", map[string]int{"days": days})
	case Has(q, "薄弱", "weak"):
		add("get_weak_topics", struct{}{})
	case Has(q, "最近", "关闭", "变化"):
		add("get_recent_changes", map[string]int{"days": 7})
	case Has(q, "关注源", "关注来源", "watched sources"):
		add("get_watched_sources", struct{}{})

	case Has(q, "投过", "applications", "申请记录"):
		add("list_applications", struct{}{})
	case Has(q, "项目", "project"):
		add("get_project_facts", struct{}{})
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
	case searchQuery != "":
		add("search_jobs", map[string]string{"query": searchQuery})
	}
	return Reply{Calls: calls}, nil
}

func demoSearchQuery(question string) string {
	if !Has(question, "岗位", "jobs") || !Has(question, "搜索", "查找", "找岗位", "找 ", "找go", "找java") {
		return ""
	}
	if strings.Contains(strings.ToLower(question), "c++") {
		return "C++"
	}
	if tech := regexp.MustCompile(`(?i)\b(?:go|java|python|rust)\b`).FindString(question); tech != "" {
		return tech
	}
	m := regexp.MustCompile(`(?:搜索|查找|找)\s*([^，。？！?]{2,40}?)\s*岗位`).FindStringSubmatch(question)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(m[1]), "的"))
}

func comparisonCompany(question string) string {
	m := regexp.MustCompile(`(?:比较|对比)\s*([^，。？！?]{1,80}?)\s*的(?:校招|后端|服务端|所有|全部|这些)?岗位`).FindStringSubmatch(question)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m[1]), "一下"))
}
