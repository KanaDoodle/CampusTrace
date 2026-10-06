package resume

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Diagnostics contain only fixed reasons and numeric positions. Never include
// resume text, model content, suggested values, credentials or personal names.
type Diagnostic struct {
	Reason       string `json:"validation_reason"`
	Scope        string `json:"scope,omitempty"`
	ProjectIndex int    `json:"project_index,omitempty"`
	ItemIndex    int    `json:"item_index,omitempty"`
}

type ValidationError struct{ Diagnostic }

func (e *ValidationError) Error() string { return "invalid resume draft: " + e.Reason }
func (e *ValidationError) Unwrap() error { return ErrInvalid }

func invalid(reason, scope string, project, item int) error {
	return &ValidationError{Diagnostic{reason, scope, project, item}}
}

func draftLimits(draft Draft) error {
	if len(draft.Suggestions) > 60 || len(draft.Projects) > 15 {
		return invalid("DRAFT_LIMIT", "", 0, 0)
	}
	count := 0
	for i, project := range draft.Projects {
		if len(project.Facts) > 20 {
			return invalid("PROJECT_FACTS_LIMIT", "PROJECT", i+1, 0)
		}
		count += len(project.Facts)
	}
	if count > 80 {
		return invalid("DRAFT_LIMIT", "", 0, 0)
	}
	return nil
}

func valueReason(value string, limit int) string {
	if strings.TrimSpace(value) == "" {
		return "VALUE_EMPTY"
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return "VALUE_INVALID"
	}
	if len(value) > limit {
		return "VALUE_LENGTH"
	}
	if HasSensitive(value) {
		return "VALUE_SENSITIVE"
	}
	return ""
}

func suggestionReason(s Suggestion) string {
	if !allowedFields[s.Field] {
		return "FIELD_UNKNOWN"
	}
	if reason := valueReason(s.Value, 120); reason != "" {
		return reason
	}
	switch s.Field {
	case "degree":
		if s.Value != "ASSOCIATE" && s.Value != "BACHELOR" && s.Value != "MASTER" && s.Value != "PHD" {
			return "VALUE_FORMAT"
		}
	case "graduation_year", "experience_months":
		n, err := strconv.Atoi(s.Value)
		if err != nil || strconv.Itoa(n) != s.Value || (s.Field == "graduation_year" && (n < 2000 || n > 2100)) || (s.Field == "experience_months" && (n < 0 || n > 600)) {
			return "VALUE_FORMAT"
		}
	}
	return ""
}

func factReason(f Fact) string {
	if f.Kind != "IMPLEMENTED" && f.Kind != "LIMITATION" && f.Kind != "PLANNED" {
		return "FACT_KIND"
	}
	return valueReason(f.Claim, 1000)
}

func exactReason(source, value string) string {
	if strings.TrimSpace(value) == "" {
		return "EXCERPT_EMPTY"
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return "EXCERPT_NOT_EXACT"
	}
	if len(value) > 600 {
		return "EXCERPT_LENGTH"
	}
	if !strings.Contains(source, value) {
		return "EXCERPT_NOT_EXACT"
	}
	return ""
}

// Validate remains strict. Review must first restore an actual contiguous slice
// of the submitted source; downstream callers never receive normalized quotes.
func Validate(draft Draft, source string) error {
	if err := draftLimits(draft); err != nil {
		return err
	}
	for i, s := range draft.Suggestions {
		reason := suggestionReason(s)
		if reason == "" {
			reason = exactReason(source, s.Excerpt)
		}
		if reason != "" {
			return invalid(reason, "SUGGESTION", 0, i+1)
		}
	}
	for i, project := range draft.Projects {
		reason := valueReason(project.Name, 200)
		if reason == "" {
			reason = exactReason(source, project.Excerpt)
		}
		if reason != "" {
			return invalid(reason, "PROJECT", i+1, 0)
		}
		for j, fact := range project.Facts {
			reason := factReason(fact)
			if reason == "" {
				reason = exactReason(source, fact.Excerpt)
			}
			if reason != "" {
				return invalid(reason, "FACT", i+1, j+1)
			}
		}
	}
	return nil
}

// Map every compact UTF-8 byte to its original rune span. Only Unicode
// whitespace is ignored; letters, casing, punctuation and numbers stay exact.
func compactSource(source string) (string, []int, []int) {
	var text strings.Builder
	starts, ends := []int{}, []int{}
	for i, r := range source {
		if unicode.IsSpace(r) {
			continue
		}
		text.WriteRune(r)
		for n := 0; n < utf8.RuneLen(r); n++ {
			starts = append(starts, i)
			ends = append(ends, i+utf8.RuneLen(r))
		}
	}
	return text.String(), starts, ends
}

func restoreExcerpt(source, compact string, starts, ends []int, value string) (string, bool, string) {
	if reason := exactReason(source, value); reason != "EXCERPT_NOT_EXACT" {
		return value, false, reason
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return "", false, "EXCERPT_NOT_EXACT"
	}
	var quote strings.Builder
	for _, r := range value {
		if !unicode.IsSpace(r) {
			quote.WriteRune(r)
		}
	}
	needle := quote.String()
	if needle == "" {
		return "", false, "EXCERPT_EMPTY"
	}
	i := strings.Index(compact, needle)
	if i < 0 {
		return "", false, "EXCERPT_NOT_EXACT"
	}
	if strings.Contains(compact[i+1:], needle) {
		return "", false, "EXCERPT_AMBIGUOUS"
	}
	actual := source[starts[i]:ends[i+len(needle)-1]]
	if reason := exactReason(source, actual); reason != "" {
		return "", false, reason
	}
	return actual, true, ""
}

func reviewDraft(draft Draft, source string) (Draft, error) {
	if err := draftLimits(draft); err != nil {
		return Draft{}, err
	}
	out := Draft{Suggestions: []Suggestion{}, Projects: []Project{}}
	compact, starts, ends := compactSource(source)
	check := func(quote string, d Diagnostic, reason string) (string, bool) {
		actual, repaired := "", false
		if reason == "" {
			actual, repaired, reason = restoreExcerpt(source, compact, starts, ends, quote)
		}
		if reason != "" {
			d.Reason = reason
			out.Warnings = append(out.Warnings, d)
			return "", false
		}
		if repaired {
			out.NormalizedExcerpts++
		}
		return actual, true
	}
	for i, suggestion := range draft.Suggestions {
		actual, ok := check(suggestion.Excerpt, Diagnostic{Scope: "SUGGESTION", ItemIndex: i + 1}, suggestionReason(suggestion))
		if ok {
			suggestion.Excerpt = actual
			out.Suggestions = append(out.Suggestions, suggestion)
		}
	}
	validFacts := 0
	for i, project := range draft.Projects {
		actual, ok := check(project.Excerpt, Diagnostic{Scope: "PROJECT", ProjectIndex: i + 1}, valueReason(project.Name, 200))
		if !ok {
			continue
		} // An unbound project cannot safely own its facts.
		kept := Project{Name: project.Name, Excerpt: actual, Facts: []Fact{}}
		for j, fact := range project.Facts {
			actual, ok := check(fact.Excerpt, Diagnostic{Scope: "FACT", ProjectIndex: i + 1, ItemIndex: j + 1}, factReason(fact))
			if ok {
				fact.Excerpt = actual
				kept.Facts = append(kept.Facts, fact)
				validFacts++
			}
		}
		out.Projects = append(out.Projects, kept)
	}
	out.Suggestions = filterSkillDetails(out.Suggestions)
	if len(out.Suggestions) == 0 && validFacts == 0 && len(out.Warnings) > 0 {
		return Draft{}, &ValidationError{out.Warnings[0]}
	}
	if err := Validate(out, source); err != nil {
		return Draft{}, err
	}
	return out, nil
}
