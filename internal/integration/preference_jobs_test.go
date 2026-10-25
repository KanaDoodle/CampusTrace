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
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestPreferenceJobsLargeProfileIndependentOwnedPages(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	o, err := s.Ingest(ctx, ingest(t, ctx, s, src))
	must(t, err)
	base, err := s.Job(ctx, o.JobID)
	must(t, err)
	other, err := s.NewUser(ctx, d.ID()+"@preference.invalid", "unused")
	must(t, err)
	ids := []string{}
	tx, err := s.DB.BeginTx(ctx, nil)
	must(t, err)
	defer tx.Rollback()
	for i := 0; i < 529; i++ {
		job, disposition := base, "SAVED"
		job.ID, job.Fingerprint = d.ID(), d.Hash(d.ID())
		job.Visibility, job.OwnerID, job.Company = "PRIVATE", u, "Synthetic Alpha"
		job.Title, job.Locations = fmt.Sprintf("Go 服务端 %03d", i), []string{"上海"}
		job.UpdatedAt = time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
		if i == 0 {
			job.Title = "100%_Go!"
		}
		if i >= 525 && i < 527 {
			disposition = "IGNORED"
		}
		if i == 527 {
			disposition = "NONE"
		}
		if i == 528 {
			// A stale preference cannot grant access after visibility changes.
			job.OwnerID, job.Title = other, "PRIVATE_SENTINEL"
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO jobs(id,company_id,fingerprint,visibility,owner_id,body) VALUES(?,?,?,?,?,?)", job.ID, job.CompanyID, job.Fingerprint, job.Visibility, job.OwnerID, d.JSON(job))
		must(t, err)
		_, err = tx.ExecContext(ctx, "INSERT INTO user_job_preferences(user_id,job_id,disposition) VALUES(?,?,?)", u, job.ID, disposition)
		must(t, err)
		ids = append(ids, job.ID)
	}
	app := d.Application{ID: d.ID(), UserID: u, JobID: ids[0], State: "PLANNED", Version: 1}
	foreign := d.Application{ID: d.ID(), UserID: other, JobID: ids[0], State: "APPLIED", Version: 1, Note: "PRIVATE_APPLICATION_SENTINEL"}
	for _, a := range []d.Application{app, foreign} {
		_, err = tx.ExecContext(ctx, "INSERT INTO applications(id,user_id,job_id,version,body) VALUES(?,?,?,?,?)", a.ID, a.UserID, a.JobID, a.Version, d.JSON(a))
		must(t, err)
	}
	must(t, tx.Commit())
	t.Cleanup(func() {
		_, e := s.DB.ExecContext(ctx, "DELETE FROM user_job_preferences WHERE user_id=? AND job_id=?", u, ids[528])
		must(t, e)
		_, e = s.DB.ExecContext(ctx, "DELETE FROM jobs WHERE id=?", ids[528])
		must(t, e)
	})
	if _, err = s.Profile(ctx, u); err == nil {
		t.Fatal("fixture must exercise browsing without a profile")
	}
	seen := map[string]bool{}
	for page := 1; page <= 11; page++ {
		v, err := s.PreferenceJobs(ctx, u, "SAVED", "", "updated", page)
		must(t, err)
		if v.Total != 525 || v.Page != page || v.PageSize != 50 || len(v.Jobs) != min(50, 525-(page-1)*50) {
			t.Fatalf("incomplete collection: total=%d page=%d rows=%d", v.Total, v.Page, len(v.Jobs))
		}
		for _, row := range v.Jobs {
			if seen[row.Job.ID] || row.Job.OwnerID != u || row.Disposition != "SAVED" {
				t.Fatal("duplicate or inaccessible preference")
			}
			seen[row.Job.ID] = true
			if row.Job.ID == app.JobID && (row.Application == nil || row.Application.ID != app.ID) {
				t.Fatal("owner application missing or replaced by another user's record")
			}
		}
	}
	last, err := s.PreferenceJobs(ctx, u, "SAVED", "", "company", 20000)
	must(t, err)
	if last.Page != 11 || len(last.Jobs) != 25 {
		t.Fatal("removed pages were not clamped")
	}
	for _, query := range []string{"%", "_", "!", "100%_Go!", "100% 上海"} {
		v, err := s.PreferenceJobs(ctx, u, "SAVED", query, "title", 1)
		must(t, err)
		if v.Total != 1 || v.Jobs[0].Job.ID != ids[0] {
			t.Fatalf("search did not treat wildcards literally: %q total=%d", query, v.Total)
		}
	}
	ignored, err := s.PreferenceJobs(ctx, u, "IGNORED", "", "updated", 1)
	must(t, err)
	if ignored.Total != 2 {
		t.Fatal("ignored scope contains saved/default preferences")
	}
	foreignPage, err := s.PreferenceJobs(ctx, other, "SAVED", "", "updated", 1)
	must(t, err)
	if foreignPage.Total != 0 {
		t.Fatal("preferences leaked across users")
	}
	authn := auth.Service{Store: s, Secret: []byte("synthetic-preference-http-secret-32")}
	token, err := authn.Token(u)
	must(t, err)
	h := (&transport.API{Store: s, Queue: q, Auth: authn}).Handler()
	read := func(url string, authenticated bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", url, nil)
		if authenticated {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	var before, after int
	counts := func() int {
		var n int
		must(t, s.DB.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM notifications WHERE user_id=?) + (SELECT COUNT(*) FROM rankings WHERE user_id=?) + (SELECT COUNT(*) FROM profiles WHERE user_id=?)", u, u, u).Scan(&n))
		return n
	}
	before = counts()
	w := read("/api/preferences/jobs?q=100%25", true)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "PRIVATE_") {
		t.Fatal(w.Code, w.Body.String())
	}
	var body p.PreferenceJobsPage
	must(t, json.Unmarshal(w.Body.Bytes(), &body))
	if body.Total != 1 || body.Jobs[0].Application.ID != app.ID {
		t.Fatal("HTTP endpoint omitted page or application metadata")
	}
	after = counts()
	if before != after {
		t.Fatal("metadata browsing wrote derived state")
	}
	if read("/api/preferences/jobs", false).Code != 401 {
		t.Fatal("preferences endpoint omitted authentication")
	}
	for _, url := range []string{"/api/preferences/jobs?page=no", "/api/preferences/jobs?page=0", "/api/preferences/jobs?sort=injected", "/api/preferences/jobs?disposition=NONE"} {
		if read(url, true).Code != 400 {
			t.Fatal("invalid preference parameters accepted", url)
		}
	}
}
