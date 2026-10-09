package agent

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const HarnessVersion = "harness-2026-10-09-v2"

type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
	Input       string   `json:"input"`
	MaxCalls    int      `json:"max_calls"`
}

// Skills are reviewed application capabilities, not executable prompt text.
// Neither a JD nor a model can install a skill or expand its authority.
func Skills() []Skill {
	return []Skill{
		{"company-choice", "同公司岗位取舍", "1", "读取完整资料与已有比较，核对岗位范围；没有有效比较时提示生成，不自行付费分析。", []string{"get_candidate_document", "search_jobs", "compare_company_jobs", "search_memories"}, "company", 4},
		{"interview-prep", "岗位面试准备", "1", "结合岗位、已核对经历、薄弱点和真实练习记录整理准备依据。", []string{"get_candidate_document", "get_preparation_context", "get_weak_topics", "get_practice_runs"}, "job_id", 4},
		{"review-plan", "复盘与复习", "1", "查看真实复盘、薄弱点、练习和相关学习材料，不把建议写成项目经历。", []string{"get_interview_history", "get_weak_topics", "get_practice_runs", "search_knowledge", "list_mcp_resources", "read_mcp_resource"}, "topic", 4},
		{"daily-review", "今天的求职待办", "1", "查看招聘变化和近期截止事项，提示下一步；不自动提交申请或触发分析。", []string{"get_agent_todos", "get_closing_jobs", "get_recent_changes", "list_applications"}, "none", 4},
	}
}
func FindSkill(id string) (Skill, bool) {
	for _, s := range Skills() {
		if s.ID == id {
			return s, true
		}
	}
	return Skill{}, false
}

type SkillInput struct {
	SkillID    string `json:"skill_id"`
	Company    string `json:"company,omitempty"`
	JobID      string `json:"job_id,omitempty"`
	Topic      string `json:"topic,omitempty"`
	RequestKey string `json:"request_key"`
}

func (v SkillInput) Validate() error {
	if _, ok := FindSkill(v.SkillID); !ok || !regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`).MatchString(v.RequestKey) {
		return errors.New("invalid skill request")
	}
	if len(v.Company) > 200 || len(v.Topic) > 500 || !utf8.ValidString(v.Company+v.Topic) || strings.ContainsAny(v.Company+v.Topic, "\x00") {
		return errors.New("invalid skill input")
	}
	switch v.SkillID {
	case "company-choice":
		if strings.TrimSpace(v.Company) == "" || v.JobID != "" || v.Topic != "" {
			return errors.New("company required")
		}
	case "interview-prep":
		if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(v.JobID) || v.Company != "" || v.Topic != "" {
			return errors.New("job required")
		}
	case "review-plan":
		if strings.TrimSpace(v.Topic) == "" || v.Company != "" || v.JobID != "" {
			return errors.New("topic required")
		}
	case "daily-review":
		if v.Company != "" || v.JobID != "" || v.Topic != "" {
			return errors.New("unexpected input")
		}
	}
	return nil
}
func (v SkillInput) Plan() []Call {
	call := func(name string, args any) Call {
		return Call{ID: d.ID(), Name: name, Args: json.RawMessage(d.JSON(args))}
	}
	empty := struct{}{}
	switch v.SkillID {
	case "company-choice":
		return []Call{call("get_candidate_document", empty), call("search_jobs", map[string]string{"query": v.Company}), call("compare_company_jobs", map[string]string{"company": v.Company}), call("search_memories", map[string]string{"query": v.Company})}
	case "interview-prep":
		return []Call{call("get_candidate_document", empty), call("get_preparation_context", map[string]string{"job_id": v.JobID}), call("get_weak_topics", empty), call("get_practice_runs", empty)}
	case "review-plan":
		return []Call{call("get_interview_history", empty), call("get_weak_topics", empty), call("get_practice_runs", empty), call("search_knowledge", map[string]string{"query": v.Topic})}
	case "daily-review":
		return []Call{call("get_agent_todos", empty), call("get_closing_jobs", map[string]int{"days": 7}), call("get_recent_changes", map[string]int{"days": 7}), call("list_applications", empty)}
	}
	return nil
}
