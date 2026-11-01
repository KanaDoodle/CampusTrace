package agent

import (
	"encoding/json"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

// A prescribed response generator exercises contracts; it does not grade real
// answers. Production always uses the configured model through LiveModel.
func controlledDefenseReply(messages []Message, control string, emit func(string, any) (Reply, error)) (Reply, error) {
	var data struct {
		Goal    d.PreparationInput    `json:"goal"`
		Sources []d.PreparationSource `json:"sources"`
		Defense d.DefenseState        `json:"defense"`
	}
	if len(messages) < 2 || json.Unmarshal([]byte(messages[1].Content), &data) != nil {
		return Reply{}, p.ErrValidation
	}
	known := map[string]d.PreparationSource{}
	for _, s := range data.Sources {
		known[s.ID] = s
	}
	rejected := false
	for _, msg := range messages[2:] {
		if msg.Role != "tool" {
			continue
		}
		rejected = rejected || strings.Contains(msg.Content, "INVALID_DEFENSE")
		var value struct {
			Sources []d.PreparationSource `json:"sources"`
		}
		if json.Unmarshal([]byte(msg.Content), &value) == nil {
			for _, src := range value.Sources {
				known[src.ID] = src
			}
		}
	}
	if control == "defense-bad-read" && !rejected {
		return emit("read_project_defense_evidence", map[string]any{"ids": []string{"../../.env"}})
	}
	if control == "strategy-cost-probe" && data.Defense.TeachingStrategy != nil && data.Defense.TeachingStrategy.Reading == "read-one" && !rejected {
		return emit("read_project_defense_evidence", map[string]any{"ids": []string{"controlled-forbidden-id"}})
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, point := range data.Defense.Points {
		id := point.Sources[0]
		if _, ok := known[id]; !ok && !seen[id] {
			missing = append(missing, id)
			seen[id] = true
		}
	}
	if len(missing) > 0 {
		return emit("read_project_defense_evidence", map[string]any{"ids": missing})
	}
	r := d.DefenseReview{Summary: "受控反馈：核对本次回答与项目证据", NextAction: "READ", NextQuestion: "请复述刚才片段中的一个动作。", Findings: []d.DefenseFinding{}, Plan: d.PreparationPlan{Summary: "本轮阅读与复述安排", Items: []d.PreparationItem{}}}
	if len(data.Defense.Memories) > 0 {
		r.NextQuestion = "上次练习留下了一个待核对点，请用当前代码重新解释这个动作。"
	}
	// Explicit controlled probes exercise the offline gate, never production
	// semantics. These choices are not evidence that a teaching style is worse.
	probe := data.Defense.TeachingStrategy
	if probe != nil && probe.Followup == "contrast" && strings.HasPrefix(control, "strategy-") {
		r.NextQuestion = "请对照解释：选择下一步与实际发送模型请求，分别由谁负责？"
	}
	for _, point := range data.Defense.Points {
		src := known[point.Sources[0]]
		quote := strings.TrimSpace(strings.Split(src.Text, "\n")[0])
		if control == "defense-invalid-quote" && !rejected {
			quote = "invented-not-in-source"
		}
		if probe != nil && ((control == "strategy-contract-probe" && probe.Explain == "observation-first") || (control == "strategy-validation-probe" && probe.Followup == "contrast")) {
			quote = "controlled-invalid-citation"
		}
		r.Findings = append(r.Findings, d.DefenseFinding{Point: point.ID, Verdict: "NOT_COVERED", AnswerQuote: "", Explanation: "脚本化反馈用于验证缺口、引用和下一步结构，不是模型质量评分。", References: []d.PreparationReference{{SourceID: src.ID, Quote: quote, Supports: "用于定位对应核对点的代码片段"}}})
	}
	if control == "defense-finish" {
		r.NextAction = "FINISH"
		r.NextQuestion = ""
		return emit("finish_project_defense_review", r)
	}
	if data.Goal.Minutes >= 15 {
		r.Plan.Items = append(r.Plan.Items, d.PreparationItem{ID: "read", Title: "阅读一个片段", Minutes: 10, Day: 1, Basis: "GENERAL", Action: "展开依据阅读该函数，记下一步动作", Deliverable: "一句话说明该动作", Workload: &d.PreparationWorkload{Mode: "READ", Units: 1, Scope: "1个片段：指定执行函数"}, References: r.Findings[0].References})
	} else {
		r.NextAction = "FOLLOW_UP"
	}
	r.Plan.Items = append(r.Plan.Items, d.PreparationItem{ID: "retell", Title: "复述一个动作", Minutes: 5, Day: 1, Basis: "GENERAL", Action: "用自己的话解释该动作", Deliverable: "一段自己的说明", Workload: &d.PreparationWorkload{Mode: "ORAL", Units: 1, Scope: "1个问题：该动作由谁完成"}, References: []d.PreparationReference{}})
	return emit("finish_project_defense_review", r)
}
