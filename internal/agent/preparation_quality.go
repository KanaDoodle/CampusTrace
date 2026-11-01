package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

const PreparationQualityVersion = "preparation-quality-v2"

// These are conservative product admission rules, not estimates of a user's
// measured learning speed. Natural-language scope and entailment still need review.
const PreparationQualityPolicy = `质量约束：引用必须说明supports：原文中哪项具体要求、机制或限制支持本项行动；口号、标语、标题、仓库简介不能代替实现证据，不相关的引用应删除。GENERAL建议没有具体材料支持时可不引用，不要为了凑引用选择口号。每项workload声明mode、units、scope。ORAL每单元是一个具体问题的复述与自查，至少5分钟；READ是一个明确片段及核对笔记，至少10分钟；MODIFY是修改并验证一个已有示例，至少15分钟；CODE是完成一道小题/小型示例及基本测试，至少30分钟。scope说明明确产物规模，units不得把多道题或多个主题合并伪装为一个单元。scope必须以与units一致的数量开头，例如“1个问题：TCP半包如何处理”；不把三个模式写为一个单元，不用“合并成一题”绕过数量。总预算15分钟及以下最多两项，每项units固定为1，只用ORAL或READ，优先一到两个核心点，不承诺新学会多主题或写出多道完整程序。算法技巧不等于设计模式，二者分开准备，设计模式须给具体模式与适用场景。检索前写gap和expected_use：已有材料缺少什么、补到后将用于哪条准备行动。已有依据足够就直接结束；15分钟最多补查一次。空结果、失败或仅返回已读来源时可有限改写；连续两次没有新来源后使用现有依据或询问。不要为了使用工具而检索，也不要为了显得检索有用而添加无关引用。`

func validatePreparationQuality(state *d.PreparationState, item d.PreparationItem, index int) error {
	if state.QualityVersion == "" {
		return nil
	}
	if state.QualityVersion != PreparationQualityVersion {
		return p.ErrValidation
	}
	w := item.Workload
	if w == nil {
		return fmt.Errorf("%w: WORKLOAD item %d requires mode, units and scope", p.ErrValidation, index)
	}
	floor := preparationWorkloadFloor(w.Mode)
	if floor == 0 || w.Units < 1 || w.Units > 12 || strings.TrimSpace(w.Scope) == "" || utf8.RuneCountInString(w.Scope) > 80 || resume.HasSensitive(w.Scope) {
		return fmt.Errorf("%w: WORKLOAD item %d has invalid mode, units or scope", p.ErrValidation, index)
	}
	if count := preparationScopeCount(w.Scope); count != w.Units {
		return fmt.Errorf("%w: WORKLOAD_SCOPE item %d scope must start with its declared unit count (%d), one concrete problem/fragment/example per unit", p.ErrValidation, index, w.Units)
	}
	if state.Input.Minutes <= 15 && w.Units != 1 {
		return fmt.Errorf("%w: WORKLOAD short tasks require one unit per item; remove lower priorities", p.ErrValidation)
	}
	if state.Input.Minutes <= 15 && w.Mode != "ORAL" && w.Mode != "READ" {
		return fmt.Errorf("%w: WORKLOAD short tasks allow ORAL or READ only", p.ErrValidation)
	}
	if item.Minutes < floor*w.Units {
		return fmt.Errorf("%w: WORKLOAD item %d mode %s x %d requires at least %d minutes; reduce units or choose a smaller output", p.ErrValidation, index, w.Mode, w.Units, floor*w.Units)
	}
	for refIndex, ref := range item.References {
		if strings.TrimSpace(ref.Supports) == "" || utf8.RuneCountInString(ref.Supports) > 100 || resume.HasSensitive(ref.Supports) {
			return fmt.Errorf("%w: CITATION_SUPPORT item %d reference %d must explain which requirement, mechanism or limitation supports the action; remove decorative quotations", p.ErrValidation, index, refIndex)
		}
	}
	return nil
}
func preparationSearchLimit(state *d.PreparationState) int {
	if state.Input.RetrievalMode == "off" {
		return 0
	}
	if state.QualityVersion != "" && state.Input.Minutes <= 15 {
		return 1
	}
	return 3
}
func preparationNoNewEvidence(state *d.PreparationState) bool {
	if state.QualityVersion == "" {
		return false
	}
	completed := 0
	for i := len(state.Retrievals) - 1; i >= 0; i-- {
		read := state.Retrievals[i]
		if read.Outcome == "DISPATCHED" {
			continue
		}
		if len(read.NewSourceIDs) > 0 {
			return false
		}
		completed++
		if completed == 2 {
			return true
		}
	}
	return false
}
func preparationSearchArgs(state *d.PreparationState, raw json.RawMessage) (json.RawMessage, d.PreparationRetrieval, error) {
	if state.QualityVersion == "" {
		return raw, d.PreparationRetrieval{}, nil
	}
	var in struct {
		Query       string `json:"query"`
		Gap         string `json:"gap"`
		ExpectedUse string `json:"expected_use"`
	}
	if d.Strict(raw, &in) != nil || strings.TrimSpace(in.Query) == "" || len(in.Query) > 1000 || strings.TrimSpace(in.Gap) == "" || len(in.Gap) > 300 || strings.TrimSpace(in.ExpectedUse) == "" || len(in.ExpectedUse) > 300 || resume.HasSensitive(in.Query+in.Gap+in.ExpectedUse) {
		return nil, d.PreparationRetrieval{}, fmt.Errorf("%w: SEARCH_JUSTIFICATION requires query, a concrete gap in available evidence, and expected_use", p.ErrValidation)
	}
	return json.RawMessage(d.JSON(map[string]string{"query": in.Query})), d.PreparationRetrieval{Query: in.Query, Gap: in.Gap, ExpectedUse: in.ExpectedUse, Outcome: "DISPATCHED", NewSourceIDs: []string{}, CitedSourceIDs: []string{}}, nil
}
func preparationRecordUse(state *d.PreparationState, plan d.PreparationPlan) {
	cited := map[string]bool{}
	for _, item := range plan.Items {
		for _, ref := range item.References {
			cited[ref.SourceID] = true
		}
	}
	for i := range state.Retrievals {
		state.Retrievals[i].CitedSourceIDs = []string{}
		for _, id := range state.Retrievals[i].NewSourceIDs {
			if cited[id] {
				state.Retrievals[i].CitedSourceIDs = append(state.Retrievals[i].CitedSourceIDs, id)
			}
		}
	}
}
func preparationQualityDefinitions(state *d.PreparationState, defs []Definition) []Definition {
	if state.QualityVersion != "" {
		fields := defs[2].Parameters["properties"].(map[string]any)["items"].(map[string]any)["items"].(map[string]any)
		props := fields["properties"].(map[string]any)
		modes := []string{"ORAL", "READ", "MODIFY", "CODE"}
		if state.Input.Minutes <= 15 {
			modes = []string{"ORAL", "READ"}
		}
		if state.Input.Minutes <= 15 {
			defs[2].Parameters["properties"].(map[string]any)["items"].(map[string]any)["maxItems"] = min(2, state.Input.Minutes/5)
		}
		props["workload"] = object(map[string]any{"mode": map[string]any{"type": "string", "enum": modes}, "units": map[string]any{"type": "integer", "minimum": 1, "maximum": preparationMaxUnits(state)}, "scope": map[string]any{"type": "string", "maxLength": 40, "description": "Start with units count, e.g. 1个问题：TCP半包如何处理. One concrete unit, no disguised bundle."}}, "mode", "units", "scope")
		fields["required"] = append(fields["required"].([]string), "workload")
		reference := props["references"].(map[string]any)["items"].(map[string]any)
		reference["properties"].(map[string]any)["supports"] = map[string]any{"type": "string", "maxLength": 40, "description": "State how the quoted requirement, mechanism or limitation supports the action. Do not use slogans or unrelated lines."}
		reference["required"] = append(reference["required"].([]string), "supports")
		for i := 0; i < 2; i++ {
			defs[i].Parameters = object(map[string]any{"query": str(), "gap": map[string]any{"type": "string", "maxLength": 80}, "expected_use": map[string]any{"type": "string", "maxLength": 80}}, "query", "gap", "expected_use")
			defs[i].Description += fmt.Sprintf(" Explain the concrete missing evidence and expected use before searching. At most %d searches for this task.", preparationSearchLimit(state))
		}
	}
	if state.Input.RetrievalMode == "off" {
		return defs[2:]
	}
	return defs
}
func preparationTerminalDefinitions(defs []Definition) []Definition {
	out := []Definition{}
	for _, def := range defs {
		if def.Name == "finish_preparation_plan" || def.Name == "ask_preparation_question" {
			out = append(out, def)
		}
	}
	return out
}

func preparationQualityContext(state *d.PreparationState) string {
	if state.Defense != nil {
		return fmt.Sprintf("本轮总时间%d分钟，只安排最多两个具体的阅读或复述事项，每项一个单元。READ至少10分钟，ORAL至少5分钟，scope以1开头明确片段或问题。引用必须说明supports。本轮不检索外部学习资料；read_project_defense_evidence用于读取核对范围内的主题代码，最多3次、每次至多3个ID。已有读取结果可复用。", state.Input.Minutes)
	}
	return PreparationQualityPolicy + fmt.Sprintf("本轮补查上限为%d，retrieval_mode=%s；只用已核对材料时直接综合，不提供检索工具。", preparationSearchLimit(state), state.Input.RetrievalMode)
}

func preparationWorkloadFloor(mode string) int {
	return map[string]int{"ORAL": 5, "READ": 10, "MODIFY": 15, "CODE": 30}[mode]
}
func preparationMaxUnits(state *d.PreparationState) int {
	if state.Input.Minutes <= 15 {
		return 1
	}
	return max(1, state.Input.Minutes/5)
}

var preparationScopePrefix = regexp.MustCompile(`^\s*([0-9]+|[一二三四五六七八九十两]+)\s*(个|道|份|段|条|题)`)

func preparationScopeCount(scope string) int {
	parts := preparationScopePrefix.FindStringSubmatch(scope)
	if len(parts) == 0 {
		return 0
	}
	if count, err := strconv.Atoi(parts[1]); err == nil {
		return count
	}
	return map[string]int{"一": 1, "二": 2, "两": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9, "十": 10}[parts[1]]
}
func preparationModelLimit(state *d.PreparationState) int {
	if state.ModelCallLimit > 0 && state.ModelCallLimit < 6 {
		return state.ModelCallLimit
	}
	return 6
}
