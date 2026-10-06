package domain

import (
	"errors"
	"regexp"
	"strings"
)

// Education belongs to one profile. IDs are stable local keys, not school IDs.
type Education struct {
	ID              string   `json:"id"`
	Degree          string   `json:"degree"`
	Majors          []string `json:"majors"`
	StartYear       int      `json:"start_year"`
	GraduationYear  int      `json:"graduation_year"`
	GraduationMonth int      `json:"graduation_month,omitempty"`
	Status          string   `json:"status"`
}

func DegreeLevel(value string) int {
	return map[string]int{"ASSOCIATE": 1, "BACHELOR": 2, "MASTER": 3, "PHD": 4}[value]
}

var educationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (e Education) Validate() error {
	if e.GraduationMonth < 0 || e.GraduationMonth > 12 || (e.GraduationMonth != 0 && e.GraduationYear == 0) {
		return errors.New("invalid graduation month")
	}
	if (e.Degree != "" && DegreeLevel(e.Degree) == 0) || (e.Status != "ENROLLED" && e.Status != "GRADUATED" && e.Status != "UNKNOWN") || len(e.Majors) > 10 {
		return errors.New("invalid education")
	}
	if (e.StartYear != 0 && (e.StartYear < 1970 || e.StartYear > 2100)) || (e.GraduationYear != 0 && (e.GraduationYear < 2000 || e.GraduationYear > 2100)) || (e.StartYear != 0 && e.GraduationYear != 0 && e.StartYear > e.GraduationYear) {
		return errors.New("invalid education dates")
	}
	for _, major := range e.Majors {
		if strings.TrimSpace(major) == "" || len(major) > 200 || strings.ContainsRune(major, '\x00') {
			return errors.New("invalid education major")
		}
	}
	return nil
}

func (p *Profile) NormalizeEducations() error {
	if len(p.Educations) > 8 {
		return errors.New("too many educations")
	}
	seen := map[string]bool{}
	for i := range p.Educations {
		e := &p.Educations[i]
		if e.Majors == nil {
			e.Majors = []string{}
		}
		if e.ID == "" {
			e.ID = ID()
		}
		if !educationID.MatchString(e.ID) || seen[e.ID] || e.Validate() != nil {
			return errors.New("invalid education")
		}
		seen[e.ID] = true
	}
	if p.PrimaryEducationID != "" && !seen[p.PrimaryEducationID] {
		return errors.New("unknown primary education")
	}
	if len(p.Educations) > 0 {
		*p = p.EducationProfile()
	}
	return nil
}

// Old scalar profiles remain readable. With education records, one selected
// record supplies degree AND graduation year; majors retain all histories.
func (p Profile) EducationProfile() Profile {
	if len(p.Educations) == 0 {
		return p
	}
	primary := p.Educations[0]
	for _, e := range p.Educations {
		if p.PrimaryEducationID != "" {
			if e.ID == p.PrimaryEducationID {
				primary = e
				break
			}
		} else if DegreeLevel(e.Degree) > DegreeLevel(primary.Degree) || (DegreeLevel(e.Degree) == DegreeLevel(primary.Degree) && e.GraduationYear > primary.GraduationYear) {
			primary = e
		}
	}
	p.PrimaryEducationID = primary.ID
	p.Degree, p.GraduationYear = primary.Degree, primary.GraduationYear
	p.GraduationMonth = primary.GraduationMonth
	if primary.GraduationYear != 0 || primary.ID != "legacy-education" {
		p.GraduationFrom, p.GraduationTo = 0, 0
	}
	p.Majors = []string{}
	seen := map[string]bool{}
	for _, e := range p.Educations {
		for _, major := range e.Majors {
			if !seen[major] {
				p.Majors = append(p.Majors, major)
				seen[major] = true
			}
		}
	}
	return p
}

// Only unambiguous degree-bound major wording narrows the history. A generic
// "本科及以上，计算机专业" remains a major check across recorded histories.
func (p Profile) MajorsForRequirement(text string) []string {
	if len(p.Educations) == 0 {
		return p.Majors
	}
	if degree := MajorDegreeScope(text); degree != "" {
		out := []string{}
		for _, e := range p.Educations {
			if e.Degree == degree {
				out = append(out, e.Majors...)
			}
		}
		return out
	}
	return p.EducationProfile().Majors
}

func MajorDegreeScope(text string) string {
	for _, item := range []struct {
		degree  string
		pattern *regexp.Regexp
	}{
		{"BACHELOR", bachelorMajor}, {"MASTER", masterMajor}, {"PHD", phdMajor}, {"ASSOCIATE", associateMajor},
	} {
		if item.pattern.MatchString(text) {
			return item.degree
		}
	}
	return ""
}

var bachelorMajor = regexp.MustCompile(`(?i)本科(?:阶段|期间|所学)?专业|undergraduate\s+major|bachelor(?:'s)?\s+major`)
var masterMajor = regexp.MustCompile(`(?i)硕士(?:阶段|期间|所学)?专业|master(?:'s)?\s+major`)
var phdMajor = regexp.MustCompile(`(?i)博士(?:阶段|期间|所学)?专业|doctoral\s+major|phd\s+major`)
var associateMajor = regexp.MustCompile(`专科(?:阶段|期间|所学)?专业`)
