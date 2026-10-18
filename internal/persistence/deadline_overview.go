package persistence

import (
	"context"
	"time"
)

type DeadlineItem struct {
	Job     ApplicationJob `json:"job"`
	At      time.Time      `json:"at"`
	Date    string         `json:"date,omitempty"`
	Planned bool           `json:"planned"`
	Saved   bool           `json:"saved"`
}

// Counts cover all verified actionable deadlines; the home panel only needs
// the first five. This endpoint neither builds rankings nor writes an inbox.
type DeadlineOverview struct {
	AsOf        time.Time      `json:"as_of"`
	Total       int            `json:"total"`
	WithinDay   int            `json:"within_day"`
	Within3Days int            `json:"within_3_days"`
	Items       []DeadlineItem `json:"items"`
	Truncated   bool           `json:"truncated"`
}

func (s *Store) DeadlineOverview(ctx context.Context, user string) (DeadlineOverview, error) {
	out := DeadlineOverview{Items: []DeadlineItem{}}
	err := s.radarSnapshot(ctx, func(q Queryer, now time.Time) error {
		rows, err := s.closingJobs(ctx, q, user, now, 7)
		if err != nil {
			return err
		}
		out.AsOf, out.Total, out.Truncated = now, len(rows), len(rows) > 5
		for _, row := range rows {
			if !row.Deadline.After(now.Add(24 * time.Hour)) {
				out.WithinDay++
			}
			if !row.Deadline.After(now.Add(72 * time.Hour)) {
				out.Within3Days++
			}
			if len(out.Items) < 5 {
				j := row.Job
				out.Items = append(out.Items, DeadlineItem{Job: ApplicationJob{ID: j.ID, CompanyID: j.CompanyID, Company: j.Company, Title: j.Title, Locations: j.Locations, JobType: j.JobType, CurrentStatus: j.CurrentStatus}, At: *row.Deadline, Date: row.DeadlineDate, Planned: row.Application != nil && row.Application.State == "PLANNED", Saved: row.Disposition == "SAVED"})
			}
		}
		return nil
	})
	return out, err
}
