package matching

import "math"

// Priority is an application triage estimate, not proof of eligibility. Related
// experience receives more weight than in the audited core score. Unknown units
// receive a neutral half point rather than being treated as mismatches.
// Bounds show the effect of those unknowns (not statistical confidence).
type Priority struct {
	Score float64 `json:"score"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

func ApplicationPriority(sections []SectionScore) *Priority {
	for _, s := range sections {
		if s.Category != "REQUIRED" || s.Total == 0 || s.Direct+s.Partial+s.Transferable == 0 {
			continue
		}
		points := float64(s.Direct) + .75*float64(s.Partial) + .6*float64(s.Transferable)
		unknown := float64(s.Total - s.Known)
		round := func(v float64) float64 { return math.Round(v/float64(s.Total)*1000) / 10 }
		return &Priority{Score: round(points + .5*unknown), Lower: round(points), Upper: round(points + unknown)}
	}
	return nil
}

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
		units := requirementUnits(reqs, category)
		s := SectionScore{Category: category, Total: len(units)}
		points := 0.0
		for _, unit := range units {
			_, _, best, value := unitComparison(unit, byID)
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
