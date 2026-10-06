package matching

import (
	"regexp"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

var mixedClauses = regexp.MustCompile(`[，,；;]`)
var foundationPart = regexp.MustCompile(`计算机系统结构|操作系统|网络(?:\s*[（(]TCP/IP[）)])?|数据结构|(?:常见)?算法`)
var protocolPart = regexp.MustCompile(`TCP/IP|UDP|FTP|HTTPS|HTTP`)
var knowledgeLevel = regexp.MustCompile(`深刻认知|深入理解|熟练掌握|熟悉|了解|理解`)

// Keep the full contiguous context for proficiency and conjunctions. Only
// explicitly conjunctive foundational knowledge is repaired locally; one-of
// languages, tools, direction groups and project facts retain their structure.
func splitTestableRequirements(items []Requirement) []Requirement {
	out := []Requirement{}
	for _, r := range items {
		if r.Category == "QUALIFICATION" || r.GroupID != "" || alternativeCue.MatchString(r.Excerpt) {
			out = append(out, r)
			continue
		}
		parts := mixedClauses.Split(r.Excerpt, -1)
		soft, technical := false, false
		for _, part := range parts {
			q := Requirement{Text: part}
			soft = soft || softOnly(q)
			technical = technical || !softOnly(q) && technicalCue.MatchString(part) && (len(localFeatures(part)) > 0 || concreteTechnicalCue.MatchString(part))
		}
		if soft && technical {
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				q := r
				q.Text, q.Excerpt, q.Aspect = part, part, "TECHNICAL"
				if softOnly(q) {
					q.Aspect = "SOFT"
				}
				out = append(out, q)
			}
			continue
		}
		level := knowledgeLevel.FindString(r.Text)
		if r.Category == "REQUIRED" && r.Aspect != "SOFT" && level != "" && strings.ContainsAny(r.Excerpt, "、与和") {
			nouns := foundationPart.FindAllString(r.Text, -1)
			if len(nouns) < 2 {
				nouns = protocolPart.FindAllString(r.Text, -1)
			}
			unique := []string{}
			for _, noun := range nouns {
				if !hasString(unique, noun) {
					unique = append(unique, noun)
				}
			}
			if len(unique) >= 2 {
				for _, noun := range unique {
					q := r
					q.Text = level + noun + "相关基础知识"
					if level == "深刻认知" {
						q.Text = "对" + noun + "相关基础知识有深刻认知"
					}
					out = append(out, q)
				}
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// An explicitly requested new comparison also benefits from cached extraction.
// Historical completed matches keep their original ability units; no judgment
// is invented for a newly split capability. Unchanged cache IDs are retained.
func PrepareCachedRequirements(items []Requirement, text string) ([]Requirement, error) {
	out := RepairQualifications(normalizeRequirements(append([]Requirement{}, items...), text), text)
	if len(out) > MaxRequirements {
		return nil, ErrCapacity
	}
	counts := map[string]int{}
	for _, r := range out {
		counts[r.ID]++
	}
	seen := map[string]bool{}
	for i, r := range out {
		if r.ID == "" || counts[r.ID] > 1 {
			out[i].ID = d.Hash(r.Category + "\n" + r.Text + "\n" + r.Excerpt + "\n" + r.Aspect + "\n" + r.GroupID)[:24]
		}
		if err := ValidateRequirement(out[i], text); err != nil {
			return nil, err
		}
		if seen[out[i].ID] {
			return nil, ErrInvalid
		}
		seen[out[i].ID] = true
	}
	return out, validateGroups(out)
}
