package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"sort"
)

type CompanyPlacement struct {
	Rank    int  `json:"rank"`
	Total   int  `json:"total"`
	Pending int  `json:"pending"`
	Tied    bool `json:"tied,omitempty"`
}

type PriorityInput struct {
	Job                   d.Job
	Local                 *LocalScreen
	State, ExcludedReason string
	Breakdown             []SectionScore
	Coverage              float64
}

// The inventory ranks technical relevance and preferences only. Eligibility
// remains a separate, current check in the full company comparison. No stored
// candidate excerpts or model calls are needed for this lightweight view.
func TechnicalCompanyPlacements(inputs []PriorityInput, p d.Profile) map[string]CompanyPlacement {
	groups := map[string][]CompanyJob{}
	pending := map[string]int{}
	for _, in := range inputs {
		if in.ExcludedReason != "" || in.Job.CurrentStatus == "CLOSED" {
			continue
		}
		row := CompanyJob{Job: in.Job, Coverage: in.Coverage}
		row.roleRank, _ = directionPreference(p, DecisionInput{Job: in.Job, Local: in.Local})
		if row.roleRank < 0 {
			continue
		}
		if in.State != "ANALYZED" {
			pending[in.Job.Company]++
			continue
		}
		row.Priority = ApplicationPriority(in.Breakdown)
		if row.Priority == nil {
			continue
		}
		row.cityRank, _ = cityPreference(p, in.Job)
		row.typeRank, _ = typePreference(p, in.Job)
		for _, section := range in.Breakdown {
			if section.Category == "BONUS" && section.Total > 0 {
				row.bonusSupport = (float64(section.Direct) + .5*float64(section.Partial) + .25*float64(section.Transferable)) / float64(section.Total)
			}
		}
		groups[in.Job.Company] = append(groups[in.Job.Company], row)
	}
	out := map[string]CompanyPlacement{}
	for company, rows := range groups {
		sort.Slice(rows, func(i, j int) bool {
			order := compareOrder(rows[i], rows[j])
			if order == 0 {
				return rows[i].Job.ID < rows[j].Job.ID
			}
			return order < 0
		})
		rank := 1
		for i, row := range rows {
			if i > 0 && compareOrder(rows[i-1], row) != 0 {
				rank = i + 1
			}
			tied := i > 0 && compareOrder(rows[i-1], row) == 0 || i+1 < len(rows) && compareOrder(row, rows[i+1]) == 0
			out[row.Job.ID] = CompanyPlacement{Rank: rank, Total: len(rows), Pending: pending[company], Tied: tied}
		}
	}
	return out
}
