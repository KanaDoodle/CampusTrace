package searchapi

import "strings"

// Frozen pre-optimization behavior for differential regression tests.
func referenceMatches(q Query, d Document) bool {
	if d.Deleted || q.Company != "" && !strings.EqualFold(q.Company, d.Company) || q.JobType != "" && q.JobType != d.JobType {
		return false
	}
	if q.City != "" {
		found := false
		for _, city := range d.Locations {
			if strings.EqualFold(city, q.City) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	text := strings.ToLower(d.Title + " " + d.Company + " " + strings.Join(d.Locations, " ") + " " + d.Text)
	has := func(term string) bool {
		if tech := Technology(term); tech != "" {
			for _, t := range d.Technologies() {
				if t == tech {
					return true
				}
			}
			return false
		}
		return strings.Contains(text, strings.ToLower(term))
	}
	terms := strings.Fields(q.Text)
	matched := len(terms) == 0
	for _, term := range terms {
		if has(term) {
			matched = true
		} else if q.Mode != "ANY" {
			return false
		}
	}
	if !matched {
		return false
	}
	for _, term := range strings.Fields(q.Exclude) {
		if has(term) {
			return false
		}
	}
	return true
}
