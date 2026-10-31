package matching

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Restore formatting-only changes to a unique literal source span. Shared by
// whole-context model responses and reviewed imports; never accept paraphrases.
func restoreSourceExcerpt(source, quote string, limit int) (string, string) {
	if strings.TrimSpace(quote) == "" {
		return "", "EXCERPT_EMPTY"
	}
	if len(quote) > limit {
		return "", "EXCERPT_LENGTH"
	}
	if !utf8.ValidString(source) || !utf8.ValidString(quote) || strings.ContainsRune(quote, '\x00') {
		return "", "EXCERPT_NOT_EXACT"
	}
	if strings.Contains(source, quote) {
		return quote, ""
	}
	var compact, needle strings.Builder
	starts, ends := []int{}, []int{}
	for i, r := range source {
		if unicode.IsSpace(r) {
			continue
		}
		compact.WriteRune(r)
		for n := 0; n < utf8.RuneLen(r); n++ {
			starts = append(starts, i)
			ends = append(ends, i+utf8.RuneLen(r))
		}
	}
	for _, r := range quote {
		if !unicode.IsSpace(r) {
			needle.WriteRune(r)
		}
	}
	text, value := compact.String(), needle.String()
	i := strings.Index(text, value)
	if i < 0 {
		return "", "EXCERPT_NOT_EXACT"
	}
	if strings.Contains(text[i+1:], value) {
		return "", "EXCERPT_AMBIGUOUS"
	}
	actual := source[starts[i]:ends[i+len(value)-1]]
	if len(actual) > limit {
		return "", "EXCERPT_LENGTH"
	}
	return actual, ""
}
