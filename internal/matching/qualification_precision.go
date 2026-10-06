package matching

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// Dates retain their own exact source context. Year-only Claim values remain
// compatible with historical cache keys; they cannot erase date precision.
type GraduationWindow struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Excerpt     string `json:"excerpt"`
	NeedsReview bool   `json:"needs_review,omitempty"`
}

const graduationDateToken = `20\d{2}(?:年|[-./])\s*\d{1,2}(?:月|[-./])(?:\s*\d{1,2}日?)?`

var graduationDateRange = regexp.MustCompile(`(` + graduationDateToken + `)\s*(?:至|到|~|～|—|–|-)\s*(` + graduationDateToken + `)(?:期间|之间|内)?(?:毕业|的?应届毕业生)`)
var graduationMetadata = regexp.MustCompile(`毕业范围开始日期\s*[:：]\s*(` + graduationDateToken + `)[^\n]*\n毕业范围结束日期\s*[:：]\s*(` + graduationDateToken + `)`)
var graduationNumbers = regexp.MustCompile(`\d+`)
var qualificationClauses = regexp.MustCompile(`[，,。；;\n]`)

func graduationDate(value string, end bool) (time.Time, bool) {
	nums := graduationNumbers.FindAllString(value, -1)
	if len(nums) < 2 || len(nums) > 3 {
		return time.Time{}, false
	}
	year, _ := strconv.Atoi(nums[0])
	month, _ := strconv.Atoi(nums[1])
	day := 1
	if len(nums) == 3 {
		day, _ = strconv.Atoi(nums[2])
	} else if end {
		day = time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return t, year >= 2000 && year <= 2100 && int(t.Month()) == month && t.Day() == day && t.Year() == year
}

func graduationWindow(text string) *GraduationWindow {
	var result *GraduationWindow
	for _, pattern := range []*regexp.Regexp{graduationDateRange, graduationMetadata} {
		for _, m := range pattern.FindAllStringSubmatch(text, -1) {
			from, a := graduationDate(m[1], false)
			to, b := graduationDate(m[2], true)
			w := &GraduationWindow{From: from.Format("2006-01-02"), To: to.Format("2006-01-02"), Excerpt: m[0], NeedsReview: !a || !b || to.Before(from)}
			if result != nil && (result.From != w.From || result.To != w.To) {
				// Keep a contiguous span proving both conflicting windows.
				start := strings.Index(text, result.Excerpt)
				end := strings.LastIndex(text, w.Excerpt) + len(w.Excerpt)
				if end > start {
					result.Excerpt = text[start:end]
				}
				result.NeedsReview = true
			} else if result == nil {
				result = w
			}
		}
	}
	return result
}

// Repair only explicit qualification context, never skill judgments. Added
// degree conditions are local and leave paid comparison identities unchanged.
func RepairQualifications(items []Requirement, text string) []Requirement {
	out := append([]Requirement{}, items...)
	hasDegree := false
	for _, r := range out {
		hasDegree = hasDegree || r.Category == "QUALIFICATION" && r.ClaimType == "EDUCATION_REQUIREMENT"
	}
	for i, r := range items {
		if r.Category != "QUALIFICATION" || r.Confidence < .8 {
			continue
		}
		if r.ClaimType == "GRADUATION_REQUIREMENT" {
			w := graduationWindow(r.Excerpt)
			if w == nil && text != "" {
				w = graduationWindow(text)
			}
			if w != nil {
				out[i].GraduationWindow = w
				out[i].Text = "毕业时间在 " + w.From + " 至 " + w.To
				if !w.NeedsReview {
					// The literal date range is more precise than a model's cohort
					// label, which may omit one of the boundary years.
					out[i].Value = w.From[:4]
					if w.To[:4] != out[i].Value {
						out[i].Value += "-" + w.To[:4]
					}
				}
			} else if degreeMinimum.MatchString(r.Excerpt) {
				out[i].Text = "毕业届别要求：" + r.Value
			}
		}
		if hasDegree {
			continue
		}
		for _, clause := range qualificationClauses.Split(r.Excerpt, -1) {
			m := degreeMinimum.FindStringSubmatch(clause)
			if m == nil || strings.Contains(clause, "优先") || strings.Contains(clause, "加分") || strings.Contains(clause, "不限") || len(degreeNames.FindAllString(clause, -1)) != 1 {
				continue
			}
			q := Requirement{ID: d.Hash("qualification-degree\n" + m[0])[:24], Category: "QUALIFICATION", Aspect: "TECHNICAL", Text: m[0], Excerpt: m[0], ClaimType: "EDUCATION_REQUIREMENT", Value: map[string]string{"专科": "ASSOCIATE", "本科": "BACHELOR", "硕士": "MASTER", "博士": "PHD"}[m[1]], Confidence: r.Confidence}
			out = append(out, q)
			hasDegree = true
			break
		}
	}
	return out
}

func checkGraduationPrecision(e *d.Eligibility, p d.Profile, reqs []Requirement) {
	for i := range e.Results {
		row := &e.Results[i]
		if row.Rule != "GRADUATION_REQUIREMENT" {
			continue
		}
		for _, req := range reqs {
			w := req.GraduationWindow
			if w == nil || req.Confidence < .8 {
				continue
			}
			row.Requirement = w.From + " 至 " + w.To
			if row.Result != "PASS" || w.NeedsReview {
				if w.NeedsReview {
					row.Result, row.Explanation = "UNKNOWN", "毕业日期范围无效或存在冲突，请核对岗位原文。"
				}
				continue
			}
			from, _ := time.Parse("2006-01-02", w.From)
			to, _ := time.Parse("2006-01-02", w.To)
			start := time.Date(p.GraduationYear, 1, 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(1, 0, -1)
			if p.GraduationYear == 0 {
				start = time.Date(p.GraduationFrom, 1, 1, 0, 0, 0, 0, time.UTC)
				end = time.Date(p.GraduationTo, 12, 31, 0, 0, 0, 0, time.UTC)
			} else if p.GraduationMonth != 0 {
				start = time.Date(p.GraduationYear, time.Month(p.GraduationMonth), 1, 0, 0, 0, 0, time.UTC)
				end = start.AddDate(0, 1, -1)
				row.Candidate = fmt.Sprintf("%d-%02d", p.GraduationYear, p.GraduationMonth)
			}
			switch {
			case end.Before(from) || start.After(to):
				row.Result, row.Explanation = "FAIL", "已保存的毕业时间不在岗位明确日期范围内。"
			case start.Before(from) || end.After(to):
				row.Result, row.Explanation = "UNKNOWN", "仅有毕业年份或月份，尚不足以核对精确日期范围；请补充预计毕业月份，边界月份需核对具体日期。"
			}
		}
	}
}
