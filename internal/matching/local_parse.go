package matching

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const LocalVersion = "local-screen-v3"

type localRequirement struct {
	Category, Mode, Excerpt string
	Terms                   []string
}
type localQualification struct {
	Kind, Value, Excerpt string
}
type localParsed struct {
	Requirements   []localRequirement
	Qualifications []localQualification
	Body           []string
	Incomplete     bool
}

var localHeading = regexp.MustCompile(`(?i)^(?:[一二三四五六七八九十0-9.、()（）\s-]*)(岗位职责|工作职责|工作内容|职责描述|responsibilities|任职要求|岗位要求|任职资格|职位要求|基本要求|资格要求|任职条件|requirements|qualifications|加分项|加分条件|优先条件|bonus|preferred|公司介绍|团队介绍|公司福利|福利待遇|about us|benefits)\s*[:：]?\s*(.*)$`)
var degreeLabel = regexp.MustCompile(`(?i)^degree:\s*(ASSOCIATE|BACHELOR|MASTER|PHD)\s*$`)
var graduationLabel = regexp.MustCompile(`(?i)^graduation:\s*(20\d{2}(?:-20\d{2})?)\s*$`)
var degreeMinimum = regexp.MustCompile(`(专科|本科|硕士|博士)\s*(?:及|或)?以上`)
var degreeNames = regexp.MustCompile(`专科|本科|硕士|博士`)
var localYears = regexp.MustCompile(`20\d{2}`)
var graduationMonths = regexp.MustCompile(`年\s*\d{1,2}\s*月|20\d{2}[-./]\d{1,2}`)

func parseLocal(text string) localParsed {
	parsed := localParsed{Requirements: []localRequirement{}, Qualifications: []localQualification{}, Body: []string{}}
	section := ""
	seen := map[string]bool{}
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if heading := localHeading.FindStringSubmatch(line); heading != nil {
			switch strings.ToLower(heading[1]) {
			case "岗位职责", "工作职责", "工作内容", "职责描述", "responsibilities":
				section = "RESPONSIBILITY"
			case "加分项", "加分条件", "优先条件", "bonus", "preferred":
				section = "BONUS"
			case "公司介绍", "团队介绍", "公司福利", "福利待遇", "about us", "benefits":
				section = "IGNORE"
			default:
				section = "REQUIRED"
			}
			line = strings.TrimSpace(heading[2])
		}
		if section == "IGNORE" || line == "" {
			continue
		}
		for _, sentence := range localSentences.Split(line, -1) {
			for _, clause := range localClauses(sentence) {
				clause = strings.TrimSpace(clause)
				if clause == "" {
					continue
				}
				if len(clause) > 600 {
					parsed.Incomplete = true
					clause = shortLocalText(clause, 600)
				}
				if localNegative.MatchString(clause) {
					continue
				}
				// Cached excerpts must not retain the backing storage of the whole
				// source description, which can be much larger than these bounds.
				clause = strings.Clone(clause)
				category := section
				if localBonus.MatchString(clause) {
					category = "BONUS"
				} else if strings.HasPrefix(strings.ToLower(clause), "tech:") || strings.HasPrefix(strings.ToLower(clause), "language:") || localCue.MatchString(clause) {
					if category != "BONUS" {
						category = "REQUIRED"
					}
				} else if category == "" && localDuty.MatchString(clause) {
					category = "RESPONSIBILITY"
				}
				if category == "RESPONSIBILITY" && len(parsed.Body) < 64 {
					parsed.Body = append(parsed.Body, clause)
				} else if category == "RESPONSIBILITY" {
					parsed.Incomplete = true
				}
				parseLocalQualifications(&parsed, clause, category)
				terms := localFeatures(clause)
				if category == "" {
					if len(terms) > 0 {
						parsed.Incomplete = true
					}
					continue
				}
				// A language alternative applies to language choices, not an adjacent
				// mandatory database or project capability in the same statement.
				languages, others := []string{}, []string{}
				for _, term := range terms {
					if termByID[term].Kind == "LANGUAGE" {
						languages = append(languages, term)
					} else {
						others = append(others, term)
					}
				}
				if len(languages) == 0 && genericLanguage.MatchString(clause) {
					languages = []string{"any_language"}
				}
				add := func(ids []string, mode string) {
					if len(ids) == 0 {
						return
					}
					keyIDs := append([]string{}, ids...)
					sort.Strings(keyIDs)
					key := category + mode + strings.Join(keyIDs, "|")
					if seen[key] {
						return
					}
					seen[key] = true
					if len(parsed.Requirements) >= 48 {
						parsed.Incomplete = true
						return
					}
					parsed.Requirements = append(parsed.Requirements, localRequirement{category, mode, clause, ids})
				}
				mode := "ALL"
				if len(languages) > 1 && (localAlternative.MatchString(clause) || strings.Contains(clause, "/")) && !strings.Contains(clause, "同时") && !strings.Contains(clause, "均") {
					mode = "ANY"
				}
				add(languages, mode)
				if len(languages) == 0 && len(others) > 1 && localAlternative.MatchString(clause) {
					add(others, "ANY")
				} else {
					for _, term := range others {
						add([]string{term}, "ALL")
					}
				}
			}
		}
	}
	return parsed
}

func parseLocalQualifications(parsed *localParsed, clause, category string) {
	add := func(kind, value string) {
		if len(parsed.Qualifications) >= 16 {
			parsed.Incomplete = true
			return
		}
		parsed.Qualifications = append(parsed.Qualifications, localQualification{kind, value, clause})
	}
	if category == "BONUS" {
		return
	}
	if m := degreeLabel.FindStringSubmatch(clause); m != nil {
		add("DEGREE", strings.ToUpper(m[1]))
		return
	}
	if m := graduationLabel.FindStringSubmatch(clause); m != nil {
		add("GRADUATION", m[1])
		return
	}
	if m := degreeMinimum.FindStringSubmatch(clause); m != nil {
		if len(degreeNames.FindAllString(clause, -1)) == 1 {
			add("DEGREE", map[string]string{"专科": "ASSOCIATE", "本科": "BACHELOR", "硕士": "MASTER", "博士": "PHD"}[m[1]])
		} else {
			parsed.Incomplete = true
		}
	}
	if !strings.Contains(clause, "毕业") && !strings.Contains(clause, "应届生") {
		return
	}
	years := localYears.FindAllString(clause, -1)
	if len(years) == 0 {
		return
	}
	if graduationMonths.MatchString(clause) {
		parsed.Incomplete = true // Candidate profiles have no graduation month.
		return
	}
	unique := map[string]bool{}
	for _, year := range years {
		unique[year] = true
	}
	years = nil
	for year := range unique {
		years = append(years, year)
	}
	sort.Strings(years)
	if len(years) == 2 && (strings.Contains(clause, "至") || strings.Contains(clause, "到") || strings.Contains(clause, "-")) {
		from, _ := strconv.Atoi(years[0])
		to, _ := strconv.Atoi(years[1])
		if to-from > 10 {
			parsed.Incomplete = true
			return
		}
		years = nil
		for year := from; year <= to; year++ {
			years = append(years, strconv.Itoa(year))
		}
	}
	add("GRADUATION", strings.Join(years, "|"))
}
