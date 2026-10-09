package agent

import (
	"sort"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
)

type ContextReport struct {
	Version        string   `json:"version"`
	SkillID        string   `json:"skill_id,omitempty"`
	MemoryIDs      []string `json:"memory_ids"`
	AvailableTools []string `json:"available_tools"`
	Bytes          int      `json:"bytes"`
	HistoryTurns   int      `json:"history_turns"`
}

func relevantMemories(question string, rows []p.AgentMemory) []p.AgentMemory {
	type ranked struct {
		row   p.AgentMemory
		score int
	}
	words := rag.Tokens(question)
	q := strings.ToLower(question)
	list := []ranked{}
	for _, v := range rows {
		score := 0
		text := strings.ToLower(v.Content + " " + v.Scope)
		if v.Kind == "CORRECTION" {
			score += 2
		}
		for _, w := range words {
			if len([]rune(w)) > 1 && strings.Contains(text, w) {
				score += 4
			}
		}
		if v.Scope != "" && strings.Contains(q, strings.ToLower(v.Scope)) {
			score += 8
		}
		// A scoped note with no relevance is not injected merely because it is recent.
		if v.Scope != "" && score <= 2 {
			continue
		}
		list = append(list, ranked{v, score})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	out := []p.AgentMemory{}
	for _, v := range list {
		if len(out) == 5 || len(d.JSON(append(out, v.row))) > 12000 {
			break
		}
		out = append(out, v.row)
	}
	return out
}
func selectDefinitions(question, skillID string, all []Definition) []Definition {
	if s, ok := FindSkill(skillID); ok {
		out := []Definition{}
		for _, def := range all {
			for _, name := range s.Tools {
				if def.Name == name {
					out = append(out, def)
					break
				}
			}
		}
		return out
	}
	var names []string
	switch {
	case Has(question, "记忆", "记住了什么", "记住：", "记住:"):
		names = []string{"search_memories", "remember_memory"}
	case Has(question, "练习结果", "练习记录"):
		names = []string{"get_practice_runs", "get_weak_topics"}
	case Has(question, "历史讨论", "讨论记录"):
		names = []string{"get_agent_tasks"}
	case Has(question, "MCP", "外部资料", "外部文档"):
		names = []string{"list_mcp_resources", "read_mcp_resource"}
	case Has(question, "待办", "值得处理", "今天", "今日"):
		names = []string{"get_agent_todos", "get_daily_digest", "get_closing_jobs", "get_recent_changes", "list_applications"}
	case Has(question, "深度分析进度", "分析任务", "匹配任务"):
		names = []string{"get_match_tasks"}
	}
	if len(names) == 0 {
		return all
	}
	out := []Definition{}
	for _, def := range all {
		for _, name := range names {
			if name == def.Name {
				out = append(out, def)
				break
			}
		}
	}
	return out
}
