package analysis

import (
	"errors"
	"regexp"
	"sort"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

var languageMention = regexp.MustCompile(`(?i)golang(?:\s*1\.\d+(?:\.\d+)?)?|go(?:\s*1\.\d+(?:\.\d+)?)?|javascript|typescript|java|python|rust|kotlin|swift|php|ruby|scala|lua|c\+\+(?:98|03|11|14|17|20|23|26)?|csharp|c#|cpp|c`)
var technologyHeading = regexp.MustCompile(`(?i)^(?:#{1,6}\s*|【)?(任职要求|任职资格|岗位要求|职位要求|必备条件|要求|加分项|优先条件|工作职责|岗位职责|职责|minimum qualifications|preferred qualifications|qualifications|requirements|responsibilities)(?:】)?\s*(?:[:：]\s*(.*))?$`)
var technologyPreferred = regexp.MustCompile(`(?i)优先|加分|\bpreferred\b|\boptional\b|nice[- ]to[- ]have|a plus|\bbonus\b`)
var technologyNegated = regexp.MustCompile(`(?i)不要求|无需|不必|不需要|不作要求|不做要求|语言不限|不限语言|not required|not necessary|no .{0,40}(?:required|necessary)`)
var technologyRequired = regexp.MustCompile(`(?i)必须|必需|必备|须具备|需掌握|需要(?:熟悉|掌握|精通|具备)|要求\s*[:：]?\s*(?:熟悉|掌握|精通|具备|会)|\bmust\b|\brequired\b|\brequires?\b`)
var technologyProficiency = regexp.MustCompile(`(?i)熟悉|掌握|精通|熟练|具备|了解|基础扎实|基础知识|proficien|knowledge|familiar|experience|fluenc|\bskills?\b`)
var technologyLearning = regexp.MustCompile(`(?i)入职后|培养|学习|willing to learn|will learn|\btraining\b|\bmentored\b`)
var technologyExamples = regexp.MustCompile(`(?i)例如|比如|诸如|不限于|不局限于|如|等|such as|e\.g\.|including|not limited to`)
var technologyContrast = regexp.MustCompile(`(?i)\bbut\b|但是|但`)
var technologyOneOf = regexp.MustCompile(`(?i)任一|任意一种|任一种|任选|其中一种|至少一门|至少一种|one of|any of|either|at least one`)
var technologyOr = regexp.MustCompile(`(?i)或者?|\bor\b`)
var technologyAnd = regexp.MustCompile(`(?i)和|且|以及|并|\band\b|[&+]`)
var technologyConditionGroup = regexp.MustCompile(`(?i)(?:任意|至少|任选)[一二两三四五六七八九十0-9]+(?:条|项)|(?:以下|下列).*(?:之一|任意一)|(?:any|at least) (?:one|two|three|[123]) of (?:the following|these)`)
var technologyPreferenceHeading = regexp.MustCompile(`(?i)(?:以下|下列).*(?:优先|尤佳|加分|不是必须)`)
var technologyLeftAtom = regexp.MustCompile(`(?i)(c\+\+(?:98|03|11|14|17|20|23|26)?|[a-z][a-z0-9_#.-]*)\s*(?:编程语言|语言)?\s*(?:或者?|和|以及|及|与|\bor\b|\band\b|[/|&+、,，])`)
var technologyRightAtom = regexp.MustCompile(`(?i)(?:或者?|和|以及|及|与|\bor\b|\band\b|[/|&+、,，])\s*(c\+\+(?:98|03|11|14|17|20|23|26)?|[a-z][a-z0-9_#.-]*)`)

type languageToken struct {
	start, end int
	name       string
}
type textSpan struct{ start, end int }

func asciiWord(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '#'
}
func languages(text string) []languageToken {
	var found []languageToken
	for _, span := range languageMention.FindAllStringIndex(text, -1) {
		if span[0] > 0 && asciiWord(text[span[0]-1]) || span[1] < len(text) && asciiWord(text[span[1]]) {
			continue
		}
		found = append(found, languageToken{span[0], span[1], d.TechnologyName(text[span[0]:span[1]])})
	}
	return found
}

func splitTechnologyClauses(text string) []textSpan {
	var clauses []textSpan
	start := 0
	appendClause := func(end int) {
		clause := text[start:end]
		// Commas often separate language lists. Split them only when a new
		// requirement/preference/negation/proficiency phrase changes scope.
		parts := strings.FieldsFunc(clause, func(r rune) bool { return r == ',' || r == '，' })
		mixed := false
		for i := 1; i < len(parts); i++ {
			part := parts[i]
			mixed = mixed || technologyPreferred.MatchString(part) || technologyNegated.MatchString(part) || technologyRequired.MatchString(part) || technologyProficiency.MatchString(part)
		}
		if !mixed {
			clauses = append(clauses, textSpan{start, end})
			return
		}
		cursor := start
		for at, r := range clause {
			if r == ',' || r == '，' {
				clauses = append(clauses, textSpan{cursor, start + at})
				cursor = start + at + len(string(r))
			}
		}
		clauses = append(clauses, textSpan{cursor, end})
	}
	var cuts []textSpan
	for at, r := range text {
		if strings.ContainsRune(";；。!?！？", r) {
			cuts = append(cuts, textSpan{at, at + len(string(r))})
		}
	}
	for _, span := range technologyContrast.FindAllStringIndex(text, -1) {
		if strings.HasPrefix(text[span[0]:], "但不限于") || strings.HasPrefix(text[span[0]:], "但不局限于") || strings.HasSuffix(text[:span[0]], "不") {
			continue
		}
		cuts = append(cuts, textSpan{span[0], span[1]})
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].start < cuts[j].start })
	for _, cut := range cuts {
		if cut.start >= start {
			appendClause(cut.start)
			start = cut.end
		}
	}
	if start < len(text) {
		appendClause(len(text))
	}
	return clauses
}

func languageExpression(text string, tokens []languageToken) (string, bool) {
	wordOr, explicitAnd := false, false
	for i := 1; i < len(tokens); i++ {
		between := text[tokens[i-1].end:tokens[i].start]
		wordOr = wordOr || technologyOr.MatchString(between)
		explicitAnd = explicitAnd || technologyAnd.MatchString(between)
	}
	allAlternatives := technologyOneOf.MatchString(text) || wordOr && !explicitAnd
	var expression strings.Builder
	for i, token := range tokens {
		if i > 0 {
			between := text[tokens[i-1].end:token.start]
			if allAlternatives || strings.ContainsAny(between, "/|") || technologyOr.MatchString(between) {
				expression.WriteByte('|')
			} else {
				expression.WriteByte('+')
			}
		}
		expression.WriteString(token.name)
	}
	unknown := false
	if len(tokens) > 1 || technologyOneOf.MatchString(text) || technologyOr.MatchString(text) {
		known := map[string]bool{}
		for _, token := range tokens {
			known[token.name] = true
		}
		for _, pattern := range []*regexp.Regexp{technologyLeftAtom, technologyRightAtom} {
			for _, atom := range pattern.FindAllStringSubmatchIndex(text, -1) {
				covered := false
				for _, token := range tokens {
					covered = covered || atom[2] >= token.start && atom[3] <= token.end
				}
				if covered {
					continue
				}
				name := d.TechnologyName(text[atom[2]:atom[3]])
				if !known[name] && !strings.Contains("|and|or|either|one|of|any|at|least|experience|knowledge|familiar|proficient|languages|language|programming|required|must|a|an|the|in|with|for|", "|"+name+"|") {
					unknown = true
				}
			}
		}
	}
	return expression.String(), unknown || wordOr && explicitAnd && !technologyOneOf.MatchString(text)
}

func onlyLanguageList(text string) bool {
	remainder := languageMention.ReplaceAllString(text, "")
	for _, word := range []string{"编程语言", "开发语言", "语言"} {
		remainder = strings.ReplaceAll(remainder, word, "")
	}
	return strings.Trim(remainder, " \t0123456789-*.、，,/|&+()（）:") == ""
}

func extractTechnologies(text string) ([]d.Claim, error) {
	var claims []d.Claim
	section, sectionStart, offset := "", 0, 0
	for _, piece := range strings.SplitAfter(text, "\n") {
		raw := strings.TrimSuffix(piece, "\n")
		line := strings.TrimSpace(raw)
		lineStart := offset + strings.Index(raw, line)
		offset += len(piece)
		if line == "" {
			continue
		}
		if colon := strings.IndexByte(line, ':'); colon >= 0 && strings.EqualFold(strings.TrimSpace(line[:colon]), "tech") {
			continue // Explicit normalized labels retain their existing format.
		}
		body, bodyStart := line, lineStart
		if indexes := technologyHeading.FindStringSubmatchIndex(line); indexes != nil {
			title := strings.ToLower(line[indexes[2]:indexes[3]])
			section, sectionStart = "required", lineStart
			if technologyPreferred.MatchString(title) || title == "加分项" || title == "优先条件" {
				section = "preferred"
			} else if strings.Contains(title, "职责") || title == "responsibilities" {
				section = "duties"
			}
			if indexes[4] < 0 {
				continue
			}
			body, bodyStart = line[indexes[4]:indexes[5]], lineStart+indexes[4]
		}
		if section == "required" || section == "conditional" {
			if technologyConditionGroup.MatchString(body) {
				section = "conditional"
			}
			if len(languages(body)) == 0 && technologyPreferenceHeading.MatchString(body) {
				section, sectionStart = "preferred", lineStart
			}
		}
		for _, span := range splitTechnologyClauses(body) {
			clause := body[span.start:span.end]
			tokens := languages(clause)
			if len(tokens) == 0 || technologyNegated.MatchString(clause) {
				continue
			}
			if len(clause) > 2000 {
				return nil, errors.New("schema: technology clause exceeds excerpt budget")
			}
			value, ambiguous := languageExpression(clause, tokens)
			explicit := technologyRequired.MatchString(clause)
			hard := explicit || (section == "required" || section == "conditional") && (technologyProficiency.MatchString(clause) || onlyLanguageList(clause))
			hard = hard && section != "duties" && section != "preferred" && !technologyPreferred.MatchString(clause) && !technologyExamples.MatchString(clause) && !technologyLearning.MatchString(clause)
			excerpt := clause
			if hard && !explicit {
				// Include the actual heading and intervening text, never splice a
				// heading onto a noncontiguous excerpt to justify a hard condition.
				if end := bodyStart + span.end; end-sectionStart <= 2000 {
					excerpt = text[sectionStart:end]
				} else {
					return nil, errors.New("schema: technology heading exceeds excerpt budget")
				}
			}
			if hard {
				prefix := "REQUIRED:"
				if ambiguous || section == "conditional" {
					prefix = "UNCERTAIN:"
				}
				value = prefix + value
			}
			if len(value) > 1000 {
				return nil, errors.New("schema: technology expression exceeds value budget")
			}
			claims = append(claims, d.Claim{Type: "TECH_STACK", Value: value, Excerpt: excerpt, Method: "RULE", Confidence: 1})
		}
	}
	return claims, nil
}
