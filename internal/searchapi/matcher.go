package searchapi

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"
)

type searchTerm struct {
	text       []byte
	technology string
}

// Matcher owns scratch space for one request's sequential catalog scan. It is
// deliberately not shared between goroutines or users. Its matching semantics
// remain strings.ToLower substring matching, including Unicode and exclusions.
type Matcher struct {
	query           Query
	terms, excludes []searchTerm
	scratch         []byte
}

func (q Query) PrepareMatcher() *Matcher {
	prepare := func(text string) []searchTerm {
		var terms []searchTerm
		for _, word := range strings.Fields(text) {
			terms = append(terms, searchTerm{text: []byte(strings.ToLower(word)), technology: Technology(word)})
		}
		return terms
	}
	return &Matcher{query: q, terms: prepare(q.Text), excludes: prepare(q.Exclude)}
}

// Append into request-owned storage instead of allocating a concatenated
// document and a second full lowercase copy for every candidate. Decode invalid
// UTF-8 as RuneError, just as strings.ToLower does on the original joined text.
func appendLower(dst []byte, s string) []byte {
	for len(s) > 0 {
		if c := s[0]; c < utf8.RuneSelf {
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			dst = append(dst, c)
			s = s[1:]
		} else {
			r, n := utf8.DecodeRuneInString(s)
			dst = utf8.AppendRune(dst, unicode.ToLower(r))
			s = s[n:]
		}
	}
	return dst
}

func (m *Matcher) Matches(d Document) bool {
	q := m.query
	if d.Deleted || q.Company != "" && !strings.EqualFold(q.Company, d.Company) || q.JobType != "" && q.JobType != d.JobType {
		return false
	}
	if q.City != "" {
		found := false
		for _, city := range d.Locations {
			if strings.EqualFold(city, q.City) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	textReady, techReady := false, false
	var tech []string
	has := func(term searchTerm) bool {
		if term.technology != "" {
			if !techReady {
				tech = d.Technologies()
				techReady = true
			}
			for _, t := range tech {
				if t == term.technology {
					return true
				}
			}
			return false
		}
		if !textReady {
			m.scratch = appendLower(m.scratch[:0], d.Title)
			m.scratch = appendLower(append(m.scratch, ' '), d.Company)
			m.scratch = append(m.scratch, ' ')
			for i, city := range d.Locations {
				if i > 0 {
					m.scratch = append(m.scratch, ' ')
				}
				m.scratch = appendLower(m.scratch, city)
			}
			m.scratch = appendLower(append(m.scratch, ' '), d.Text)
			textReady = true
		}
		return bytes.Contains(m.scratch, term.text)
	}
	matched := len(m.terms) == 0
	for _, term := range m.terms {
		if has(term) {
			matched = true
		} else if q.Mode != "ANY" {
			return false
		}
	}
	if !matched {
		return false
	}
	for _, term := range m.excludes {
		if has(term) {
			return false
		}
	}
	return true
}
