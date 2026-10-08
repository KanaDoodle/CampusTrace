package matching

import (
	"math"
	"regexp"
	"strings"
)

var sectionHeading = regexp.MustCompile(`^(?:[一二三四五六七八九十0-9]+[、.．]\s*)?(?:专业能力|专业技能|专业领域|研究方向|技术能力|技术要求|技术深度|专业要求|能力要求|能力特质|基本要求|基础要求|通用要求|技能要求|知识与技能要求|素质要求|工作要求|任职要求|任职资格|岗位要求|岗位职责|工作职责|职责描述|岗位描述|岗位亮点|薪酬福利|加分项|优先条件|优选条件|教育背景|专业背景|学历要求|综合素质|个人素质|职业素养|其他要求|AI\s*能力|AI\s*能力要求|AI通用能力|AI应用技能)[：:]$`)

// Only a known, standalone source heading with no value or selection rule is
// formatting. A heading followed by actual conditions is never discarded.
func isSectionHeading(r Requirement, source string) bool {
	label := strings.TrimSpace(r.Text)
	if !sectionHeading.MatchString(label) || len(r.Text) > 600 || len(r.Excerpt) > 600 || !strings.Contains(r.Excerpt, label) || !strings.Contains(source, r.Excerpt) || r.Value != "" || r.GroupID != "" || r.GroupExcerpt != "" || r.GraduationWindow != nil || math.IsNaN(r.Confidence) || r.Confidence < 0 || r.Confidence > 1 {
		return false
	}
	if r.Category != "QUALIFICATION" && r.Category != "REQUIRED" && r.Category != "BONUS" && r.Category != "RESPONSIBILITY" || r.Aspect != "" && r.Aspect != "TECHNICAL" && r.Aspect != "SOFT" {
		return false
	}
	quotedLine := false
	for _, line := range strings.Split(r.Excerpt, "\n") {
		quotedLine = quotedLine || strings.TrimSpace(line) == label
	}
	if !quotedLine {
		return false
	}
	for _, line := range strings.Split(source, "\n") {
		if strings.TrimSpace(line) == label {
			return true
		}
	}
	return false
}

func omitSectionHeadings(items []Requirement, source string) []Requirement {
	out := []Requirement{}
	for _, r := range items {
		if !isSectionHeading(r, source) {
			out = append(out, r)
		}
	}
	return out
}

func ignorableChatHeading(r Requirement, source string, c Candidate, matches []Match) bool {
	if !isSectionHeading(r, source) {
		return false
	}
	found := []Match{}
	for _, m := range matches {
		if m.RequirementID == r.ID {
			found = append(found, m)
		}
	}
	return len(found) == 1 && found[0].Result == "NO_EVIDENCE" && len(found[0].Evidence) == 0 && found[0].ReviewNote == "" && validateMatches(c, []Requirement{r}, found, false) == nil
}
