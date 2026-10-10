package transport

import (
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

// Inventory cards only need browsing and ranking fields. Detailed evidence and
// the complete reviewed candidate are read by the detail/export endpoints.
type inventoryJob struct {
	p.ApplicationJob
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type inventoryLocal struct {
	Version   string   `json:"version"`
	Score     float64  `json:"score"`
	Tier      string   `json:"tier"`
	Role      string   `json:"role"`
	Reasons   []string `json:"reasons,omitempty"`
	Direction struct {
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
	} `json:"direction"`
}

type inventoryRow struct {
	Job              inventoryJob               `json:"job"`
	Cities           []string                   `json:"cities"`
	TextBytes        int                        `json:"text_bytes"`
	RequirementsKey  string                     `json:"requirements_key"`
	InputKey         string                     `json:"input_key"`
	PreliminaryScore float64                    `json:"preliminary_score"`
	Local            *inventoryLocal            `json:"local,omitempty"`
	ExcludedReason   string                     `json:"excluded_reason"`
	State            string                     `json:"state"`
	Score            *float64                   `json:"score"`
	Priority         *matching.Priority         `json:"priority,omitempty"`
	CompanyPlacement *matching.CompanyPlacement `json:"company_placement,omitempty"`
	Coverage         float64                    `json:"coverage"`
	Breakdown        []matching.SectionScore    `json:"breakdown,omitempty"`
	AnalysisMode     string                     `json:"analysis_mode,omitempty"`
	Holistic         *struct {
		Fit string `json:"fit"`
	} `json:"holistic,omitempty"`
	Source      string              `json:"source,omitempty"`
	Disposition string              `json:"disposition"`
	Application *p.MatchApplication `json:"application,omitempty"`
	Campaign    *p.MatchCampaign    `json:"campaign,omitempty"`
}

type inventoryFact struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type inventorySnapshot struct {
	Candidate struct {
		Facts []inventoryFact `json:"facts"`
	} `json:"candidate"`
	CandidateHash string              `json:"candidate_hash"`
	Jobs          []inventoryRow      `json:"jobs"`
	Settings      matching.Settings   `json:"settings"`
	CallsToday    int                 `json:"calls_today"`
	CityAliases   map[string][]string `json:"city_aliases"`
}

func inventoryPreview(v p.MatchSnapshot) inventorySnapshot {
	out := inventorySnapshot{CandidateHash: v.CandidateHash, Jobs: make([]inventoryRow, 0, len(v.Jobs)), Settings: v.Settings, CallsToday: v.CallsToday, CityAliases: v.CityAliases}
	out.Candidate.Facts = []inventoryFact{}
	for _, f := range v.Candidate.Facts {
		if f.Kind == "ROLE" || f.Kind == "CITY_PREFERRED" || f.Kind == "CITY_ACCEPTABLE" {
			out.Candidate.Facts = append(out.Candidate.Facts, inventoryFact{f.Kind, f.Text})
		}
	}
	for _, row := range v.Jobs {
		j := row.Job
		r := inventoryRow{Job: inventoryJob{ApplicationJob: p.ApplicationJob{ID: j.ID, CompanyID: j.CompanyID, Company: j.Company, Title: j.Title, Locations: j.Locations, JobType: j.JobType, CurrentStatus: j.CurrentStatus}, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt}, Cities: row.Cities, TextBytes: row.TextBytes, RequirementsKey: row.RequirementsKey, InputKey: row.InputKey, PreliminaryScore: row.PreliminaryScore, ExcludedReason: row.ExcludedReason, State: row.State, Score: row.Score, Priority: row.Priority, CompanyPlacement: row.CompanyPlacement, Coverage: row.Coverage, Breakdown: row.Breakdown, AnalysisMode: row.AnalysisMode, Source: row.Source, Disposition: row.Disposition, Application: row.Application, Campaign: row.Campaign}
		if local := row.Local; local != nil {
			r.Local = &inventoryLocal{Version: local.Version, Score: local.Score, Tier: local.Tier, Role: local.Role}
			r.Local.Direction.Status = local.Direction.Status
			if len(local.Reasons) > 0 {
				r.Local.Reasons = local.Reasons[:1]
			} else {
				r.Local.Direction.Reason = local.Direction.Reason
			}
		}
		if row.Holistic != nil {
			r.Holistic = &struct {
				Fit string `json:"fit"`
			}{row.Holistic.Fit}
		}
		out.Jobs = append(out.Jobs, r)
	}
	return out
}
