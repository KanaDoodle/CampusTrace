package persistence

import (
	"context"
	"sort"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const radarCatalogLimit = 10000
const radarPreviewLimit = 5

func hasUpcomingDeadline(v d.RadarJob, now time.Time, days int) bool {
	return actionable(v) && v.Deadline != nil && v.Deadline.After(now) && !v.Deadline.After(now.Add(time.Duration(days)*24*time.Hour))
}

// Retain only the best display rows while counting the complete SQL snapshot.
// The ID tie-breaker makes the result independent of keyset batch boundaries.
func radarPreview(rows []d.RadarJob, row d.RadarJob, before func(a, b d.RadarJob) bool) []d.RadarJob {
	if len(rows) == radarPreviewLimit && !before(row, rows[len(rows)-1]) {
		return rows
	}
	rows = append(rows, row)
	sort.Slice(rows, func(i, j int) bool { return before(rows[i], rows[j]) })
	if len(rows) > radarPreviewLimit {
		rows[radarPreviewLimit] = d.RadarJob{}
		rows = rows[:radarPreviewLimit]
	}
	return rows
}

func addDigestJob(v *d.DailyDigest, row d.RadarJob, now time.Time) {
	if row.Job.CreatedAt.After(now.Add(-24 * time.Hour)) {
		v.Counts["new_jobs"]++
		v.NewJobs = radarPreview(v.NewJobs, row, func(a, b d.RadarJob) bool {
			if a.Job.CreatedAt.Equal(b.Job.CreatedAt) {
				return a.Job.ID < b.Job.ID
			}
			return a.Job.CreatedAt.After(b.Job.CreatedAt)
		})
	}
	if recommended(row) {
		v.Counts["recommended_jobs"]++
		v.RecommendedJobs = radarPreview(v.RecommendedJobs, row, func(a, b d.RadarJob) bool {
			if a.Ranking.Score == b.Ranking.Score {
				return a.Job.ID < b.Job.ID
			}
			return a.Ranking.Score > b.Ranking.Score
		})
	}
	if hasUpcomingDeadline(row, now, 7) {
		v.Counts["closing_soon"]++
		v.ClosingSoon = radarPreview(v.ClosingSoon, row, func(a, b d.RadarJob) bool {
			if a.Deadline.Equal(*b.Deadline) {
				return a.Job.ID < b.Job.ID
			}
			return a.Deadline.Before(*b.Deadline)
		})
	}
}

func (s *Store) DailyDigest(ctx context.Context, user string) (d.DailyDigest, error) {
	v := d.DailyDigest{Counts: map[string]int{"new_jobs": 0, "recommended_jobs": 0, "closing_soon": 0, "status_changes": 0, "upcoming_interviews": 0}, NewJobs: []d.RadarJob{}, RecommendedJobs: []d.RadarJob{}, ClosingSoon: []d.RadarJob{}, StatusChanges: []d.ChangeItem{}}
	if user == "" {
		return v, ErrNotFound
	}
	err := s.radarSnapshot(ctx, func(q Queryer, now time.Time) error {
		v.AsOf = now
		var count int
		if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?)", user).Scan(&count); err != nil {
			return err
		}
		if count > radarCatalogLimit {
			return ErrRadarCapacity
		}
		profile, err := s.radarProfile(ctx, q, user)
		if err != nil {
			return err
		}
		cursor := ""
		for {
			jobs, err := Many[d.Job](ctx, q, `SELECT j.body FROM jobs j
 LEFT JOIN user_job_preferences p ON p.job_id=j.id AND p.user_id=?
 WHERE j.id>? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))
 AND COALESCE(p.disposition,'NONE')<>'IGNORED'
 ORDER BY j.id LIMIT 100`, user, cursor, user)
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				break
			}
			rows, err := s.radarBatchRowsWithProfile(ctx, q, user, now, jobs, profile)
			if err != nil {
				return err
			}
			for _, row := range rows {
				addDigestJob(&v, row, now)
			}
			cursor = jobs[len(jobs)-1].ID
		}
		v.RecentChanges, err = recentChanges(ctx, q, user, 1, now, 10001)
		if err != nil {
			return err
		}
		if len(v.RecentChanges) > 10000 {
			return ErrRadarCapacity
		}
		for _, c := range v.RecentChanges {
			if c.Type == "JOB_STATUS_CHANGED" {
				v.Counts["status_changes"]++
				if len(v.StatusChanges) < 10 {
					v.StatusChanges = append(v.StatusChanges, c)
				}
			}
		}
		v.UpcomingInterviews, err = upcoming(ctx, q, user, now)
		if err != nil {
			return err
		}
		v.Counts["upcoming_interviews"] = len(v.UpcomingInterviews)
		v.Truncated = v.Counts["new_jobs"] > radarPreviewLimit || v.Counts["recommended_jobs"] > radarPreviewLimit || v.Counts["closing_soon"] > radarPreviewLimit || v.Counts["status_changes"] > 10 || len(v.RecentChanges) > 10 || len(v.UpcomingInterviews) > 10
		v.RecentChanges = v.RecentChanges[:min(10, len(v.RecentChanges))]
		v.UpcomingInterviews = v.UpcomingInterviews[:min(10, len(v.UpcomingInterviews))]
		return nil
	})
	// An incomplete read is never exposed as a successful partial summary.
	if err != nil {
		return d.DailyDigest{}, err
	}
	return v, nil
}
