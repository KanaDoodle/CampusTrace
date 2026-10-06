package resume

import (
	"regexp"
	"strings"
)

func projectContentReason(source, value string, limit int) string {
	if reason := valueReason(value, limit); reason != "" {
		return reason
	}
	return exactReasonLimit(source, value, limit)
}

func containsBullet(bullets []string, value string) bool {
	for _, bullet := range bullets {
		if bullet == value {
			return true
		}
	}
	return false
}

// Project headings delimit the source block. Content from a later project must
// not become the current project's overview or supporting bullet.
func projectBlock(source, heading string, projects []Project) string {
	start := strings.Index(source, heading)
	end := len(source)
	for _, other := range projects {
		if other.Excerpt == heading || other.Excerpt == "" {
			continue
		}
		if i := strings.Index(source[start+len(heading):], other.Excerpt); i >= 0 {
			if next := start + len(heading) + i; next < end {
				end = next
			}
		}
	}
	return source[start:end]
}

var bulletMarker = regexp.MustCompile(`^[\t ]*(?:[•●▪◦·][\t ]*|[-*][\t ]+|[0-9]{1,2}[.)、][\t ]*|[（(][0-9]{1,2}[）)][\t ]*)`)
var sourceSection = regexp.MustCompile(`^[\t ]*(?:教育经历|工作经历|实习经历|专业技能|技术栈|技能清单|项目描述|项目简介)[：:]`)

// When the source has explicit bullet markers, they are stronger boundaries
// than the model's guessed fragments. Preserve contiguous wrapped lines. Plain
// paragraphs without markers are left alone rather than guessing PDF layout.
func markedSourceBullets(block string) []string {
	units := []string{}
	start, offset := -1, 0
	finish := func(end int) {
		if start >= 0 {
			text := strings.TrimSpace(block[start:end])
			if text != "" {
				units = append(units, text)
			}
			start = -1
		}
	}
	for _, line := range strings.SplitAfter(block, "\n") {
		if marker := bulletMarker.FindStringIndex(line); marker != nil {
			finish(offset)
			start = offset + marker[1]
		} else if strings.TrimSpace(line) == "" || sourceSection.MatchString(line) {
			finish(offset)
		}
		offset += len(line)
	}
	finish(len(block))
	return units
}

func alignSourceBullets(block string, proposed []string) []string {
	units := markedSourceBullets(block)
	out := []string{}
	for _, bullet := range proposed {
		owner := -1
		for i, unit := range units {
			if strings.Contains(unit, bullet) {
				if owner >= 0 {
					owner = -1
					break
				}
				owner = i
			}
		}
		if owner >= 0 && len(units[owner]) <= 2000 && !HasSensitive(units[owner]) {
			bullet = units[owner]
		}
		if !containsBullet(out, bullet) {
			out = append(out, bullet)
		}
	}
	return out
}

// Never expand a completed-work claim across wording that could describe a
// plan, negation, qualification or another person's work. Mixed-type bullets
// keep their separately quoted clauses. This is a conservative guard, not a
// replacement for the model's classification or the user's review.
var incompleteBullet = regexp.MustCompile(`(?i)计划|规划|拟|待|将来|未来|下一步|后续|未|没有|不|无|局限|不足|仅|只|团队|他人|希望|准备|考虑|目标|planned?|planning|future|todo|not\b|never\b|without\b|limitation|team|will\b|would\b|intend|propos|only\b`)

func completeAccomplishments(facts []Fact, bullets []string) []Fact {
	owners := make([]int, len(facts))
	groups := map[int][]int{}
	for i, fact := range facts {
		owners[i] = -1
		for j, bullet := range bullets {
			if strings.Contains(bullet, fact.Excerpt) {
				if owners[i] >= 0 {
					owners[i] = -1 // Repeated clauses cannot select a unique bullet.
					break
				}
				owners[i] = j
			}
		}
		if owners[i] >= 0 {
			groups[owners[i]] = append(groups[owners[i]], i)
		}
	}
	eligible := map[int]bool{}
	for bullet, indexes := range groups {
		ok := !incompleteBullet.MatchString(bullets[bullet])
		for _, i := range indexes {
			ok = ok && facts[i].Kind == "IMPLEMENTED"
		}
		eligible[bullet] = ok
	}
	out := []Fact{}
	seen := map[int]bool{}
	for i, fact := range facts {
		bullet := owners[i]
		if eligible[bullet] {
			if seen[bullet] {
				continue
			}
			seen[bullet] = true
			// Copy a verified continuous source span; never join model summaries
			// or fabricate connective words, scale or results.
			fact.Claim, fact.Excerpt = bullets[bullet], bullets[bullet]
		}
		out = append(out, fact)
	}
	return out
}
