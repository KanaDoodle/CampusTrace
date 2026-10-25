package integration

import (
	"fmt"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestRadarChangesWindowPreservesHistoricalPredecessorAndTies(t *testing.T) {
	ctx, s, _, u, src := radarSetup(t)
	_, err := s.DB.ExecContext(ctx, "UPDATE sources SET body=JSON_SET(body,'$.visibility','PRIVATE','$.owner_id',?) WHERE id=?", u, src)
	must(t, err)
	o, err := s.Ingest(ctx, ingest(t, ctx, s, src))
	must(t, err)
	now := time.Now().UTC()
	_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET body=JSON_SET(body,'$.created_at',?) WHERE id=?", now.Add(-10*24*time.Hour).Format(time.RFC3339Nano), o.JobID)
	must(t, err)
	statuses := []struct {
		hours  int
		status string
	}{{-100, "OPEN"}, {-30, "OPEN"}, {-23, "CLOSED"}, {-22, "CLOSED"}, {-21, "OPEN"}, {-20, "OPEN"}, {-1, "CLOSED"}, {-1, "OPEN"}, {2, "CLOSED"}}
	prefix := d.ID()[:24]
	wanted := map[string][2]string{}
	for i, item := range statuses {
		at := now.Add(time.Duration(item.hours) * time.Hour)
		// JSON offsets are compared as instants; SQL orders the UTC column.
		v := d.Assessment{ID: prefix + fmt.Sprintf("%08d", i), JobID: o.JobID, Status: item.status, AssessedAt: at.In(time.FixedZone("fixture-east", 14*3600))}
		_, err = s.DB.ExecContext(ctx, "INSERT INTO assessments(id,job_id,assessed_at,body) VALUES(?,?,?,?)", v.ID, v.JobID, at, d.JSON(v))
		must(t, err)
		if i > 0 && item.hours >= -24 && item.hours <= 0 && statuses[i-1].status != item.status {
			wanted[v.ID] = [2]string{statuses[i-1].status, item.status}
		}
	}
	rows, err := s.RecentChanges(ctx, u, 1)
	must(t, err)
	seen := map[string]bool{}
	for _, row := range rows {
		if row.Type != "JOB_STATUS_CHANGED" {
			continue
		}
		pair, ok := wanted[row.ID]
		if !ok || row.From != pair[0] || row.To != pair[1] || seen[row.ID] {
			t.Fatalf("invalid window transition: %+v", row)
		}
		seen[row.ID] = true
	}
	if len(seen) != 4 || len(seen) != len(wanted) {
		t.Fatalf("lost historical predecessor or same-time transition: %d/%d", len(seen), len(wanted))
	}
	digest, err := s.DailyDigest(ctx, u)
	must(t, err)
	if digest.Counts["status_changes"] != len(wanted) || len(digest.StatusChanges) != len(wanted) {
		t.Fatal("digest disagrees with changes window")
	}
	for i := 0; i < 2; i++ {
		_, _, err = s.RefreshNotifications(ctx, u)
		must(t, err)
	}
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.type'))='JOB_STATUS_CHANGED'", u).Scan(&count))
	if count != len(wanted) {
		t.Fatal("window transition notifications lost or repeated", count)
	}
	must(t, s.SetPreference(ctx, u, o.JobID, "IGNORED"))
	rows, err = s.RecentChanges(ctx, u, 1)
	must(t, err)
	if len(rows) != 0 {
		t.Fatal("ignored job leaked into window read")
	}
}
