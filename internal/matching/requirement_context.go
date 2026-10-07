package matching

import (
	"regexp"
	"strings"
)

var agentParts = regexp.MustCompile(`(?i)Tool\s+Use|Planning|Memory|Reflection`)
var ragParts = regexp.MustCompile(`文档解析|分块策略|向量化|检索排序`)

func capabilityParts(text string) []string {
	pattern := agentParts
	if !agentParts.MatchString(text) {
		pattern = ragParts
	}
	parts := []string{}
	for _, part := range pattern.FindAllString(text, -1) {
		if !hasString(parts, part) {
			parts = append(parts, part)
		}
	}
	return parts
}

// Recover a unique source sentence before asking for new comparisons. A short
// label such as Memory or 有实际搭建经验 must retain its governing subject and
// proficiency. Ambiguous or overlong context is left for model review.
func restoreRequirementContext(items []Requirement, source string) []Requirement {
	for i, r := range items {
		aiSubject := strings.Contains(strings.ToLower(r.Text), "agent") || strings.Contains(strings.ToLower(r.Text), "rag")
		if r.Category == "QUALIFICATION" || r.Excerpt == "" || (!agentParts.MatchString(r.Text) && !ragParts.MatchString(r.Text) && !practicalExperience.MatchString(r.Text) && !(aiSubject && knowledgeLevel.MatchString(r.Text))) {
			continue
		}
		context := ""
		for _, sentence := range clauseSeparator.Split(source, -1) {
			sentence = strings.TrimSpace(sentence)
			if !strings.Contains(sentence, r.Excerpt) {
				continue
			}
			if context != "" { // More than one source location is ambiguous.
				context = ""
				break
			}
			context = sentence
		}
		if context == "" || len(context) > 600 {
			continue
		}
		items[i].Excerpt = context
		parts := capabilityParts(r.Text)
		if len(parts) == 1 && !alternativeCue.MatchString(context) {
			at := strings.Index(strings.ToLower(context), strings.ToLower(parts[0]))
			if at >= 0 {
				levels := knowledgeLevel.FindAllString(context[:at], -1)
				if len(levels) > 0 {
					// The nearest preceding qualifier governs this component.
					level := levels[len(levels)-1]
					if current := knowledgeLevel.FindString(r.Text); current != "" {
						items[i].Text = strings.Replace(r.Text, current, level, 1)
					}
				}
			}
		}
	}
	return items
}

// Only repair an explicit conjunctive component list for a new comparison.
// Completed chat judgments are not cloned across freshly created abilities.
func splitAIRequirements(items []Requirement) []Requirement {
	out := []Requirement{}
	for _, r := range items {
		parts := capabilityParts(r.Text)
		level := knowledgeLevel.FindString(r.Text)
		if r.Category == "QUALIFICATION" || r.Aspect == "SOFT" || r.GroupID != "" || alternativeCue.MatchString(r.Excerpt) || len(parts) < 2 || level == "" {
			out = append(out, r)
			continue
		}
		for _, part := range parts {
			q := r
			subject := "RAG "
			if agentParts.MatchString(part) {
				subject = "Agent "
			}
			q.Text = level + subject + part + "机制"
			out = append(out, q)
		}
	}
	return withoutCoveredUmbrellas(out)
}

// An explicit umbrella and all of its listed component conditions are one
// description of the same capabilities, not extra scoring weight. Only omit
// the umbrella when every named component has its own same-context item.
func withoutCoveredUmbrellas(items []Requirement) []Requirement {
	out := []Requirement{}
	for _, r := range items {
		parts := capabilityParts(r.Excerpt)
		umbrella := r.Category != "QUALIFICATION" && r.GroupID == "" && len(parts) >= 2 && len(capabilityParts(r.Text)) == 0 && (strings.Contains(r.Excerpt, "包括") || strings.Contains(r.Excerpt, "包含")) && (strings.Contains(strings.ToLower(r.Text), "agent") || strings.Contains(strings.ToLower(r.Text), "rag")) && knowledgeLevel.MatchString(r.Text)
		covered := umbrella
		for _, part := range parts {
			found := false
			for _, other := range items {
				own := capabilityParts(other.Text)
				if other.ID != r.ID && other.Category == r.Category && other.GroupID == "" && other.Excerpt == r.Excerpt && len(own) == 1 && strings.EqualFold(own[0], part) {
					found = true
					break
				}
			}
			covered = covered && found
		}
		if !covered {
			out = append(out, r)
		}
	}
	return out
}

func proficiencyRank(text string) int {
	switch knowledgeLevel.FindString(text) {
	case "深刻认知", "深入理解", "熟练掌握":
		return 3
	case "熟悉":
		return 2
	case "了解", "理解":
		return 1
	}
	return 0
}

func repairChatContext(reqs []Requirement, matches []Match, text string) ([]Requirement, []Match) {
	contextual := restoreRequirementContext(append([]Requirement{}, reqs...), text)
	raised := map[string]bool{}
	for i, r := range contextual {
		raised[r.ID] = proficiencyRank(r.Text) > proficiencyRank(reqs[i].Text)
	}
	contextual = withoutCoveredUmbrellas(contextual)
	kept := map[string]bool{}
	for _, r := range contextual {
		kept[r.ID] = true
	}
	out := []Match{}
	for _, m := range matches {
		if !kept[m.RequirementID] {
			continue
		}
		if raised[m.RequirementID] && m.Result == "DIRECT" {
			m.Result, m.ReviewNote = "PARTIAL", ContextUnconfirmed
			m.Explanation = "已有相关依据，可作部分匹配参考；原岗位包含更高的熟悉程度要求，已恢复完整原文上下文。当前结论尚不能确认达到该程度，投递前可核实自己能否深入讲解。"
		}
		out = append(out, m)
	}
	return contextual, out
}
