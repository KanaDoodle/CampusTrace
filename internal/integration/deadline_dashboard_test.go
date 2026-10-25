package integration

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestDeadlineDashboardLargeCatalogAndNotificationWindows(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	_, sourceErr := s.DB.ExecContext(ctx, "UPDATE sources SET body=JSON_SET(body,'$.visibility','PRIVATE','$.owner_id',?) WHERE id=?", u, src)
	must(t, sourceErr)
	in := ingest(t, ctx, s, src)
	in.Text = jd + "\ndeadline: " + time.Now().Add(6*time.Hour).UTC().Format(time.RFC3339)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", o.ID)))
	o, err = p.One[d.Observation](ctx, s.DB, "SELECT body FROM observations WHERE id=?", o.ID)
	must(t, err)
	base, err := p.One[d.Job](ctx, s.DB, "SELECT body FROM jobs WHERE id=?", o.JobID)
	must(t, err)
	post, err := p.One[d.Posting](ctx, s.DB, "SELECT body FROM postings WHERE id=?", o.PostingID)
	must(t, err)
	evidence, err := p.Many[d.Evidence](ctx, s.DB, "SELECT body FROM evidence WHERE observation_id=?", o.ID)
	must(t, err)
	ids, observations := []string{base.ID}, []string{o.ID}
	// Build a large private catalog from a successfully parsed fixture. No model,
	// source network or live personal database is involved in these inserts.
	tx, err := s.DB.BeginTx(ctx, nil)
	must(t, err)
	defer tx.Rollback()
	for i := 1; i < 525; i++ {
		j, posting, obs := base, post, o
		j.ID, j.Fingerprint = d.ID(), d.Hash(d.ID())
		j.Title, j.CreatedAt = fmt.Sprintf("Synthetic deadline %03d", i), time.Now().UTC().Add(-48*time.Hour)
		posting.ID, posting.JobID = d.ID(), j.ID
		obs.ID, obs.JobID, obs.PostingID = d.ID(), j.ID, posting.ID
		_, err = tx.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,visibility,owner_id,body) VALUES(?,?,?,?,?,?)", j.ID, j.CompanyID, j.Fingerprint, "PRIVATE", u, d.JSON(j))
		must(t, err)
		_, err = tx.ExecContext(ctx, "INSERT INTO postings(id,job_id,source_id,source_key,body) VALUES(?,?,?,?,?)", posting.ID, j.ID, src, d.Hash(posting.ID), d.JSON(posting))
		must(t, err)
		_, err = tx.ExecContext(ctx, "INSERT INTO observations(id,posting_id,job_id,observed_at,body) VALUES(?,?,?,?,?)", obs.ID, posting.ID, j.ID, obs.ObservedAt, d.JSON(obs))
		must(t, err)
		for _, e := range evidence {
			e.ID, e.JobID, e.ObservationID = d.ID(), j.ID, obs.ID
			if e.Type == "DEADLINE" {
				e.Value = time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO evidence(id,observation_id,job_id,body) VALUES(?,?,?,?)", e.ID, obs.ID, j.ID, d.JSON(e))
			must(t, err)
		}
		ids, observations = append(ids, j.ID), append(observations, obs.ID)
	}
	must(t, tx.Commit())
	view, err := s.DeadlineOverview(ctx, u)
	must(t, err)
	if view.Total != 525 || view.WithinDay != 1 || view.Within3Days != 525 || len(view.Items) != 5 || !view.Truncated || view.Items[0].Job.ID != o.JobID {
		t.Fatalf("large dashboard incomplete: %+v", view)
	}
	// The optional digest must also work above the old 500-job limit. A profile
	// is optional for reading new jobs/deadlines, and never fabricated for ranking.
	digest, err := s.DailyDigest(ctx, u)
	must(t, err)
	if digest.Counts["new_jobs"] != 1 || digest.Counts["recommended_jobs"] != 0 || digest.Counts["closing_soon"] != 525 || len(digest.ClosingSoon) != 5 || !digest.Truncated {
		t.Fatalf("large digest without a profile incomplete: %+v", digest.Counts)
	}
	must(t, s.SaveProfile(ctx, u, d.Profile{GraduationYear: 2027, Degree: "BACHELOR", Skills: []string{"go"}, PreferredTypes: []string{"FULL_TIME"}, PreferredCities: []string{"Shanghai"}}))
	digest, err = s.DailyDigest(ctx, u)
	must(t, err)
	if digest.Counts["recommended_jobs"] != 525 || len(digest.RecommendedJobs) != 5 {
		t.Fatalf("recommendations lost across batches: %+v", digest.Counts)
	}
	must(t, s.SetPreference(ctx, u, ids[1], "IGNORED"))
	raw, err := s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: ids[2]})))
	must(t, err)
	var app d.Application
	must(t, json.Unmarshal(raw, &app))
	_, err = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, Version: app.Version, State: "APPLIED"})))
	must(t, err)
	digest, err = s.DailyDigest(ctx, u)
	must(t, err)
	if digest.Counts["recommended_jobs"] != 523 || digest.Counts["closing_soon"] != 523 {
		t.Fatalf("digest ignored preferences or submitted applications: %+v", digest.Counts)
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE observations SET body=JSON_SET(body,'$.fetch_status','BLOCKED') WHERE id=?", observations[3])
	must(t, err)
	stale := time.Now().UTC().Add(-8 * 24 * time.Hour)
	_, err = s.DB.ExecContext(ctx, "UPDATE observations SET observed_at=?,body=JSON_SET(body,'$.observed_at',?) WHERE id=?", stale, stale.Format(time.RFC3339Nano), observations[4])
	must(t, err)
	_, err = s.DB.ExecContext(ctx, "UPDATE observations SET body=JSON_SET(body,'$.active_analysis_generation',2,'$.current_analysis_generation',1) WHERE id=?", observations[5])
	must(t, err)
	for index, hours := range map[int]int{6: -2, 8: 480} {
		_, err = s.DB.ExecContext(ctx, "UPDATE evidence SET body=JSON_SET(body,'$.value',?) WHERE job_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.type'))='DEADLINE'", time.Now().Add(time.Duration(hours)*time.Hour).UTC().Format(time.RFC3339), ids[index])
		must(t, err)
	}
	future := time.Now().UTC().Add(time.Hour)
	_, err = s.DB.ExecContext(ctx, "UPDATE observations SET observed_at=?,body=JSON_SET(body,'$.observed_at',?) WHERE id=?", future, future.Format(time.RFC3339Nano), observations[7])
	must(t, err)
	view, err = s.DeadlineOverview(ctx, u)
	must(t, err)
	if view.Total != 517 || view.WithinDay != 1 {
		t.Fatalf("invalid/user-handled deadlines included: %+v", view)
	}
	all, err := s.ClosingJobs(ctx, u, 7)
	must(t, err)
	if len(all) != 517 {
		t.Fatalf("closing list truncated across keyset batches: %d", len(all))
	}
	_, _, err = s.RefreshNotifications(ctx, u)
	must(t, err)
	_, _, err = s.RefreshNotifications(ctx, u)
	must(t, err)
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM notifications WHERE user_id=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.type'))='JOB_CLOSING_SOON'", u).Scan(&count))
	if count != 517 {
		t.Fatalf("notifications lost or repeated above 500 jobs: %d", count)
	}
	other, err := s.NewUser(ctx, d.ID()+"@deadline.invalid", "unused")
	must(t, err)
	private, err := s.DeadlineOverview(ctx, other)
	must(t, err)
	if private.Total != 0 {
		t.Fatal("deadline dashboard leaked private jobs")
	}
	privateDigest, err := s.DailyDigest(ctx, other)
	must(t, err)
	if privateDigest.Counts["new_jobs"] != 0 || privateDigest.Counts["recommended_jobs"] != 0 || privateDigest.Counts["closing_soon"] != 0 || len(privateDigest.NewJobs) != 0 {
		t.Fatal("digest leaked private jobs")
	}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-deadline-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	r := httptest.NewRequest("GET", "/api/radar/deadlines", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "candidate") || strings.Contains(w.Body.String(), "evidence") {
		t.Fatal(w.Code, w.Body.String())
	}
	unauth := httptest.NewRecorder()
	h.ServeHTTP(unauth, httptest.NewRequest("GET", "/api/radar/deadlines", nil))
	if unauth.Code != 401 {
		t.Fatal("deadline endpoint omitted authentication")
	}
	// A large authenticated read returns bounded previews without materializing
	// another notification, assessment or ranking as a side effect.
	var before, after int
	readCounts := func() int {
		var n int
		must(t, s.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM notifications WHERE user_id=?) + (SELECT COUNT(*) FROM rankings WHERE user_id=?) + (SELECT COUNT(*) FROM eligibilities WHERE user_id=?)", u, u, u).Scan(&n))
		return n
	}
	before = readCounts()
	r = httptest.NewRequest("GET", "/api/radar/digest", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	after = readCounts()
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || before != after {
		t.Fatalf("large digest read failed or changed records: status=%d counts=%d/%d", w.Code, before, after)
	}
	var response d.DailyDigest
	must(t, json.Unmarshal(w.Body.Bytes(), &response))
	if response.Counts["closing_soon"] != 517 || len(response.ClosingSoon) != 5 || len(response.RecommendedJobs) > 5 {
		t.Fatal("large HTTP digest lost full counts or bounded previews")
	}
}

func TestDeadlineDashboardDateOnlyAndChangedPosting(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	_, sourceErr := s.DB.ExecContext(ctx, "UPDATE sources SET body=JSON_SET(body,'$.visibility','PRIVATE','$.owner_id',?) WHERE id=?", u, src)
	must(t, sourceErr)
	date := time.Now().UTC().Add(48 * time.Hour).Format("2006-01-02")
	in := ingest(t, ctx, s, src)
	in.Text = jd + "\ndeadline: " + date
	o, err := s.Ingest(ctx, in)
	must(t, err)
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", o.ID)))
	view, err := s.DeadlineOverview(ctx, u)
	must(t, err)
	if view.Total != 1 || view.Items[0].Date != date {
		t.Fatalf("source date-only wording lost: %+v", view)
	}
	// New failed source observations invalidate the old successful deadline.
	in.FetchStatus, in.Text, in.ObservedAt = "BLOCKED", "", time.Now().UTC().Add(time.Second)
	_, err = s.Ingest(ctx, in)
	must(t, err)
	view, err = s.DeadlineOverview(ctx, u)
	must(t, err)
	if view.Total != 0 {
		t.Fatal("failed latest observation revived an older deadline")
	}
}
