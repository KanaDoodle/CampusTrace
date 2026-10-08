package matching

import (
	"regexp"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

var alternativeCue = regexp.MustCompile(`(?i)一个或多个|至少一个|至少一项|任选|任一|任意一|其中之一|one or more|one of|at least one|either`)
var preferredCue = regexp.MustCompile(`(?i)优先(?:考虑|录用)?(?:[，。；,.;\s]|$)|加分(?:项)?|preferred|a plus`)
var softCue = regexp.MustCompile(`(?i)责任心|自驱|沟通|协作|合作精神|学习能力|主动学习|逻辑思维|价值观|热爱|热情|兴趣|好奇|拥抱新技术|teamwork|communication|motivation`)
var technicalCue = regexp.MustCompile(`(?i)掌握|熟悉|了解|理解|实现|开发|建设|设计|优化|使用|借助|排查|定位|经验|proficien|familiar|implement|develop`)
var concreteTechnicalCue = regexp.MustCompile(`操作系统|数据结构|算法|网络|数据库|分布式|系统架构|代码质量|自动化测试|工程化(?:的)?排查(?:思路|方法)|日志|监控|trace|Code Review`)
var directionLine = regexp.MustCompile(`^\s*[0-9]+[、.．]\s*[^：:]+方向[：:]`)
var clauseSeparator = regexp.MustCompile(`[。；;\n]`)
var genericExperience = regexp.MustCompile(`^(有)?(相关|实际)?(开发|项目|工作)?经验(者)?(优先|加分)$`)

func softOnly(r Requirement) bool {
	return softCue.MatchString(r.Text) && (!technicalCue.MatchString(r.Text) || len(localFeatures(r.Text)) == 0 && !concreteTechnicalCue.MatchString(r.Text))
}

// Correct explicit preference/attitude cues even when a model mislabels them.
// The selectable-direction repair is limited to an explicit heading and its
// numbered direction lines; a shared engineering duty is not an alternative.
func normalizeRequirements(items []Requirement, text string) []Requirement {
	return splitAIRequirements(splitTestableRequirements(restoreRequirementContext(normalizeRequirementSemantics(omitSectionHeadings(items, text), text), text)))
}

func normalizeRequirementSemantics(items []Requirement, text string) []Requirement {
	heading, lines := "", []string{}
	active := false
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "一个或多个方向") {
			heading, active = line, true
			continue
		}
		if strings.Contains(line, "任职") || strings.Contains(line, "加分项") {
			active = false
		}
		if active && directionLine.MatchString(line) {
			lines = append(lines, line)
		}
	}
	for i := range items {
		items[i] = repairUnrestrictedMajor(items[i])
		r := &items[i]
		preferred := preferredCue.MatchString(r.Text)
		if r.ClaimType == "MAJOR_REQUIREMENT" {
			for _, clause := range clauseSeparator.Split(r.Excerpt, -1) {
				if strings.Contains(clause, "专业") && preferredCue.MatchString(clause) {
					preferred = true
				}
			}
		}
		if preferred && (r.Category == "QUALIFICATION" || r.Category == "REQUIRED") {
			r.Category, r.ClaimType, r.Value = "BONUS", "", ""
		}
		if softOnly(*r) {
			r.Aspect = "SOFT"
		} else if r.Aspect == "" {
			r.Aspect = "TECHNICAL"
		}
		if r.Category == "RESPONSIBILITY" && heading != "" && r.Excerpt != "" {
			for _, line := range lines {
				if strings.Contains(line, r.Excerpt) {
					r.GroupID, r.GroupExcerpt = "selectable-directions", heading
				}
			}
		}
		if r.GroupID != "" && r.GroupExcerpt != "" {
			r.GroupID = d.Hash(r.Category + "\n" + r.Aspect + "\n" + r.GroupExcerpt)[:24]
		}
	}
	return items
}

func validateGroups(items []Requirement) error {
	groups := map[string]Requirement{}
	for _, r := range items {
		if r.GroupID == "" {
			continue
		}
		if prev, ok := groups[r.GroupID]; ok && (prev.Category != r.Category || prev.Aspect != r.Aspect || prev.GroupExcerpt != r.GroupExcerpt) {
			return ErrInvalid
		}
		groups[r.GroupID] = r
	}
	return nil
}

func isPreference(kind string) bool {
	return kind == "ROLE" || kind == "CITY_PREFERRED" || kind == "CITY_ACCEPTABLE" || kind == "JOB_TYPE_PREFERENCE"
}

// Preferences use exact saved values and deterministic aliases. They cannot
// become a model's invented technical achievement or eligibility rejection.
func comparePreferences(c Candidate, reqs []Requirement, matches []Match) []Match {
	byID := map[string]Requirement{}
	for _, r := range reqs {
		byID[r.ID] = r
	}
	for i := range matches {
		m := &matches[i]
		r := byID[m.RequirementID]
		if r.Category != "QUALIFICATION" || (r.ClaimType != "LOCATION" && r.ClaimType != "JOB_TYPE") || r.Value == "" {
			continue
		}
		*m = Match{RequirementID: m.RequirementID, Result: "NO_EVIDENCE", Explanation: "岗位与已保存偏好需要确认；偏好不同不代表没有投递资格。", Evidence: []Citation{}}
		preferred, acceptable := []Fact{}, []Fact{}
		for _, f := range c.Facts {
			if r.ClaimType == "LOCATION" && (f.Kind == "CITY_PREFERRED" || f.Kind == "CITY_ACCEPTABLE") {
				acceptable = append(acceptable, f)
				if f.Kind == "CITY_PREFERRED" {
					preferred = append(preferred, f)
				}
			} else if r.ClaimType == "JOB_TYPE" && f.Kind == "JOB_TYPE_PREFERENCE" {
				acceptable = append(acceptable, f)
			}
		}
		if len(acceptable) == 0 {
			m.Explanation = "求职资料尚未填写相应偏好。"
			continue
		}
		for _, f := range append(preferred, acceptable...) {
			matched := r.Value == f.Text
			if r.ClaimType == "LOCATION" {
				matched = d.CityAlternatives(r.Value, []string{f.Text})
			}
			if matched {
				m.Result, m.Evidence = "DIRECT", []Citation{{ID: f.ID, Excerpt: f.Text}}
				m.Explanation = "岗位类型符合已保存的偏好。"
				if f.Kind == "CITY_PREFERRED" {
					m.Explanation = "岗位包含你已保存的首选城市。"
				} else if f.Kind == "CITY_ACCEPTABLE" {
					m.Explanation = "岗位包含你已保存的可接受城市。"
				}
				break
			}
		}
	}
	return matches
}
