package matching

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxComparisonExcerpt = 600

// Only the API wire format changes. Candidate facts and the cache identity keep
// their original text, so previously verified results remain reusable.
type comparisonExcerpt struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type comparisonFact struct {
	ID          string              `json:"id"`
	Kind        string              `json:"kind"`
	ProjectName string              `json:"project_name,omitempty"`
	Excerpts    []comparisonExcerpt `json:"excerpts"`
}

type comparisonModelCandidate struct {
	Revision    uint64           `json:"revision"`
	Facts       []comparisonFact `json:"facts"`
	Limitations []comparisonFact `json:"limitations"`
}

// excerpt is accepted only as a strictly grounded compatibility fallback. The
// prompt requests excerpt_id; saved results always contain actual source text.
type comparisonCitation struct {
	ID        string `json:"id"`
	ExcerptID string `json:"excerpt_id,omitempty"`
	Excerpt   string `json:"excerpt,omitempty"`
}

func candidateWithExcerpts(c Candidate) comparisonModelCandidate {
	base := candidateForComparison(c)
	v := comparisonModelCandidate{Revision: base.Revision, Facts: []comparisonFact{}, Limitations: []comparisonFact{}}
	convert := func(facts []Fact) []comparisonFact {
		out := make([]comparisonFact, 0, len(facts))
		for _, f := range facts {
			out = append(out, comparisonFact{ID: f.ID, Kind: f.Kind, ProjectName: f.ProjectName, Excerpts: factExcerpts(f.Text)})
		}
		return out
	}
	v.Facts, v.Limitations = convert(base.Facts), convert(base.Limitations)
	return v
}

// Partition without dropping, duplicating or rewriting any source character.
// Prefer a sentence/line boundary in the latter half of each bounded chunk;
// otherwise cut at a UTF-8 rune boundary. All chunks retain the same fact kind.
func factExcerpts(source string) []comparisonExcerpt {
	out := []comparisonExcerpt{}
	for len(source) > 0 {
		end := len(source)
		if end > maxComparisonExcerpt {
			end = maxComparisonExcerpt
			for !utf8.RuneStart(source[end]) {
				end--
			}
			boundary := 0
			for i, r := range source[:end] {
				if i >= maxComparisonExcerpt/2 && strings.ContainsRune("\n。！？；.!?;", r) {
					boundary = i + utf8.RuneLen(r)
				}
			}
			if boundary > 0 {
				end = boundary
			}
		}
		out = append(out, comparisonExcerpt{ID: "e" + strconv.Itoa(len(out)+1), Text: source[:end]})
		source = source[end:]
	}
	return out
}

func resolveComparisonCitations(c Candidate, reqs []Requirement, wire []comparisonMatch, matches []Match) error {
	facts := map[string]Fact{}
	for _, f := range c.Facts {
		facts[f.ID] = f
	}
	requirements := map[string]Requirement{}
	for _, r := range reqs {
		requirements[r.ID] = r
	}
	for i, m := range wire {
		r := requirements[m.RequirementID]
		if r.Category == "QUALIFICATION" && (r.ClaimType == "LOCATION" || r.ClaimType == "JOB_TYPE") && r.Value != "" {
			// comparePreferences has already replaced this entire model judgment.
			continue
		}
		if len(m.Evidence) > 8 {
			return invalid("EVIDENCE_COUNT", i+1)
		}
		matches[i].Evidence = []Citation{}
		for _, e := range m.Evidence {
			f, known := facts[e.ID]
			if !known {
				return invalid("FACT_UNKNOWN", i+1)
			}
			quote := ""
			if e.ExcerptID != "" {
				if e.Excerpt != "" {
					return invalid("EXCERPT_REFERENCE_CONFLICT", i+1)
				}
				for _, excerpt := range factExcerpts(f.Text) {
					if excerpt.ID == e.ExcerptID {
						quote = excerpt.Text
						break
					}
				}
				if quote == "" {
					return invalid("EXCERPT_ID_UNKNOWN", i+1)
				}
			} else {
				var reason string
				quote, reason = restoreComparisonExcerpt(f.Text, e.Excerpt)
				if reason != "" {
					return invalid(reason, i+1)
				}
			}
			matches[i].Evidence = append(matches[i].Evidence, Citation{ID: e.ID, Excerpt: quote})
		}
	}
	return nil
}

// Compatibility restoration tolerates whitespace formatting only, and only
// when it identifies one source span. No paraphrases, aliases or punctuation
// changes are accepted. The final stored quote is a literal source substring.
func restoreComparisonExcerpt(source, quote string) (string, string) {
	if strings.TrimSpace(quote) == "" {
		return "", "EXCERPT_EMPTY"
	}
	if len(quote) > maxComparisonExcerpt {
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
	if len(actual) > maxComparisonExcerpt {
		return "", "EXCERPT_LENGTH"
	}
	return actual, ""
}
