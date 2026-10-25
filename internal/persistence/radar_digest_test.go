package persistence

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestDigestPreviewMatchesFullSortAcrossBatches(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	rows := make([]d.RadarJob, 1201)
	for i := range rows {
		at := now.Add(time.Duration(1+i%9) * time.Hour)
		rows[i] = d.RadarJob{Job: d.Job{ID: fmt.Sprintf("job-%04d", i), CreatedAt: now.Add(-time.Duration(i%31) * time.Hour), CurrentStatus: "OPEN"}, Eligibility: "ELIGIBLE", Ranking: d.Ranking{Score: float64(i % 17), InputIdentity: "fixture"}, Deadline: &at}
		if i%7 == 0 {
			rows[i].Application = &d.Application{State: "APPLIED"}
		}
		if i%11 == 0 {
			rows[i].Job.CurrentStatus = "CLOSED"
		}
		if i%13 == 0 {
			rows[i].Eligibility = "UNKNOWN"
		}
	}
	// Shuffled input ensures neither an early batch nor the last batch receives
	// preference; equal timestamps and scores must still use the stable ID order.
	rand.New(rand.NewSource(23)).Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
	got := d.DailyDigest{Counts: map[string]int{}}
	for _, row := range rows {
		addDigestJob(&got, row, now)
		if len(got.NewJobs) > 5 || len(got.RecommendedJobs) > 5 || len(got.ClosingSoon) > 5 {
			t.Fatal("preview grew with catalog size")
		}
	}
	newRows, recommendedRows := []d.RadarJob{}, []d.RadarJob{}
	for _, row := range rows {
		if row.Job.CreatedAt.After(now.Add(-24 * time.Hour)) {
			newRows = append(newRows, row)
		}
		if recommended(row) {
			recommendedRows = append(recommendedRows, row)
		}
	}
	sort.Slice(newRows, func(i, j int) bool {
		if newRows[i].Job.CreatedAt.Equal(newRows[j].Job.CreatedAt) {
			return newRows[i].Job.ID < newRows[j].Job.ID
		}
		return newRows[i].Job.CreatedAt.After(newRows[j].Job.CreatedAt)
	})
	sort.Slice(recommendedRows, func(i, j int) bool {
		if recommendedRows[i].Ranking.Score == recommendedRows[j].Ranking.Score {
			return recommendedRows[i].Job.ID < recommendedRows[j].Job.ID
		}
		return recommendedRows[i].Ranking.Score > recommendedRows[j].Ranking.Score
	})
	closingRows := closing(rows, now, 7)
	for _, tc := range []struct {
		name     string
		got, all []d.RadarJob
	}{{"new_jobs", got.NewJobs, newRows}, {"recommended_jobs", got.RecommendedJobs, recommendedRows}, {"closing_soon", got.ClosingSoon, closingRows}} {
		if got.Counts[tc.name] != len(tc.all) {
			t.Errorf("%s count=%d want=%d", tc.name, got.Counts[tc.name], len(tc.all))
		}
		ids := func(rows []d.RadarJob) []string {
			out := []string{}
			for _, row := range rows {
				out = append(out, row.Job.ID)
			}
			return out
		}
		if !reflect.DeepEqual(ids(tc.got), ids(tc.all[:5])) {
			t.Errorf("%s top five differ from the full sort", tc.name)
		}
	}
}
