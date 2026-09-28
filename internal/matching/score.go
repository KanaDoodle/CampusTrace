package matching

import "math"

type SectionScore struct {
	Category     string   `json:"category"`
	Score        *float64 `json:"score"`
	Coverage     float64  `json:"coverage"`
	Total        int      `json:"total"`
	Known        int      `json:"known"`
	Direct       int      `json:"direct"`
	Partial      int      `json:"partial"`
	Transferable int      `json:"transferable"`
	Missing      int      `json:"missing"`
	Mismatch     int      `json:"mismatch"`
}

// Core score is technical REQUIRED only. Duties, bonuses and attitudes have
// independent coverage; one selectable group occupies one comparison unit.
func ScoreBreakdown(reqs []Requirement, matches []Match) []SectionScore {
	byID := map[string]Match{}
	for _, m := range matches {
		byID[m.RequirementID] = m
	}
	sections := []SectionScore{}
	for _, category := range []string{"REQUIRED", "RESPONSIBILITY", "BONUS", "SOFT"} {
		units := [][]Requirement{}
		groups := map[string]int{}
		for _, r := range reqs {
			if r.Category == "QUALIFICATION" {
				continue
			}
			section := r.Category
			if r.Aspect == "SOFT" || softOnly(r) {
				section = "SOFT"
			}
			if section != category {
				continue
			}
			if r.GroupID != "" {
				if index, ok := groups[r.GroupID]; ok {
					units[index] = append(units[index], r)
					continue
				}
				groups[r.GroupID] = len(units)
			}
			units = append(units, []Requirement{r})
		}
		s := SectionScore{Category: category, Total: len(units)}
		points := 0.0
		for _, unit := range units {
			best, value, allMismatch := "NO_EVIDENCE", -1.0, true
			for _, r := range unit {
				m := byID[r.ID]
				if r.Confidence < .8 || m.Result != "MISMATCH" {
					allMismatch = false
				}
				v, positive := map[string]float64{"DIRECT": 1, "PARTIAL": .5, "TRANSFERABLE": .25}[m.Result]
				if r.Confidence >= .8 && positive && v > value {
					best, value = m.Result, v
				}
			}
			if best == "NO_EVIDENCE" && allMismatch {
				best, value = "MISMATCH", 0
			}
			switch best {
			case "DIRECT":
				s.Direct++
			case "PARTIAL":
				s.Partial++
			case "TRANSFERABLE":
				s.Transferable++
			case "MISMATCH":
				s.Mismatch++
			default:
				s.Missing++
				continue
			}
			s.Known++
			points += value
		}
		if s.Total > 0 {
			s.Coverage = math.Round(float64(s.Known)/float64(s.Total)*1000) / 10
		}
		if category != "SOFT" && s.Total > 0 && float64(s.Known)/float64(s.Total) >= .6 {
			v := math.Round(points/float64(s.Known)*1000) / 10
			s.Score = &v
		}
		sections = append(sections, s)
	}
	return sections
}
