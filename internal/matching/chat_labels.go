package matching

import (
	"regexp"
	"strings"
)

var collaborationClause = regexp.MustCompile(`^(?:与|和|同).*(?:团队|同事).*(?:沟通|协作|合作)[。\s]*$`)

// Restore only future-duty labels whose exact excerpts lie entirely inside a
// single explicit requirements/bonus section. A quote repeated across sections
// or spanning a section boundary is ambiguous and retains the model label.
func restoreChatCategories(items []Requirement, source string) ([]Requirement, int) {
	type span struct {
		start, end int
		category   string
	}
	spans := []span{}
	start, offset, category := 0, 0, ""
	for _, line := range strings.SplitAfter(source, "\n") {
		if h := localHeading.FindStringSubmatch(strings.TrimSpace(line)); h != nil && strings.TrimSpace(h[2]) == "" {
			spans = append(spans, span{start, offset, category})
			start, category = offset+len(line), localHeadingCategory(h[1])
		}
		offset += len(line)
	}
	spans = append(spans, span{start, len(source), category})
	out := append([]Requirement{}, items...)
	count := 0
	for i, r := range items {
		if r.Category != "RESPONSIBILITY" || r.GroupID != "" || r.Excerpt == "" {
			continue
		}
		resolved, ambiguous := "", false
		for at := 0; at <= len(source)-len(r.Excerpt); {
			n := strings.Index(source[at:], r.Excerpt)
			if n < 0 {
				break
			}
			n += at
			found := ""
			for _, s := range spans {
				if n >= s.start && n+len(r.Excerpt) <= s.end {
					found = s.category
					break
				}
			}
			if found != "REQUIRED" && found != "BONUS" || resolved != "" && resolved != found {
				ambiguous = true
				break
			}
			resolved = found
			at = n + 1
		}
		if !ambiguous && resolved != "" {
			out[i].Category = resolved
			count++
		}
	}
	return out, count
}

// A generic "at least one listed language" restatement adds no condition when
// the same sentence already has a precise one-of language item. Keep the latter
// and its own judgment; never merge unrelated excerpts or framework/experience
// conditions into a language label.
func coveredLanguageUmbrellas(items []Requirement) map[string]bool {
	ignored := map[string]bool{}
	for _, r := range items {
		if r.Category == "QUALIFICATION" || r.Aspect == "SOFT" || r.GroupID != "" || r.Text != "掌握至少一种/一门岗位列举的主流编程语言" {
			continue
		}
		for _, other := range items {
			if other.ID == r.ID || other.Category != r.Category || other.Aspect != r.Aspect || other.GroupID != "" || other.Excerpt != r.Excerpt || !strings.Contains(other.Excerpt, strings.TrimSpace(other.Text)) || !localAlternative.MatchString(other.Text) {
				continue
			}
			terms := localFeatures(other.Text)
			languages := len(terms) >= 2
			for _, term := range terms {
				languages = languages && termByID[term].Kind == "LANGUAGE"
			}
			if languages && !practicalExperience.MatchString(other.Text) {
				ignored[r.ID] = true
				break
			}
		}
	}
	return ignored
}

// A technical label can repeat a soft clause already represented by its own
// same-source item. Narrow the existing label without creating new abilities
// or copying its judgment to the independently evaluated soft item.
func removeCoveredSoftClauses(items []Requirement) []Requirement {
	out := append([]Requirement{}, items...)
	for i, r := range items {
		if r.Category == "QUALIFICATION" || r.Aspect == "SOFT" || r.GroupID != "" {
			continue
		}
		parts := mixedClauses.Split(r.Text, -1)
		kept := []string{}
		removed := false
		for _, part := range parts {
			part = strings.TrimSpace(part)
			covered := false
			if softOnly(Requirement{Text: part}) && (!technicalCue.MatchString(part) || collaborationClause.MatchString(part)) {
				for _, other := range items {
					label := strings.TrimSpace(other.Text)
					if other.ID != r.ID && other.Category == r.Category && other.Aspect == "SOFT" && other.GroupID == "" && other.Excerpt == r.Excerpt && label != "" && softOnly(other) && strings.Contains(part, label) {
						covered = true
						break
					}
				}
			}
			if covered {
				removed = true
			} else {
				kept = append(kept, part)
			}
		}
		if removed && strings.TrimSpace(strings.Join(kept, "")) != "" {
			out[i].Text = strings.Join(kept, "，")
		}
	}
	return out
}
