package integration

import (
	"bytes"
	"encoding/json"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRadarHTTPOwnershipAndAssets(t *testing.T) {
	ctx, s, q, u, src := radarSetup(t)
	v := radarWatch(t, ctx, s, u, src)
	b, err := s.NewUser(ctx, d.ID()+"@http-radar.invalid", "unused")
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-radar-http-auth-secret-32")}
	aToken, err := authn.Token(u)
	must(t, err)
	bToken, err := authn.Token(b)
	must(t, err)
	api := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{{"GET", "/api/watches/" + v.ID, bToken, "", 404}, {"DELETE", "/api/watches/" + v.ID, bToken, "", 404}, {"PUT", "/api/watches/" + v.ID, bToken, d.JSON(v.WatchInput), 404}, {"GET", "/api/watches/" + v.ID, aToken, "", 200}, {"GET", "/api/watches", "", "", 401}, {"GET", "/api/radar/closing?days=abc", aToken, "", 400}, {"GET", "/api/radar/changes?days=3", aToken, "", 400}, {"POST", "/api/watches", aToken, `{"source_id":"x","check_interval":300,"enabled":true,"user_id":"other"}`, 400}, {"GET", "/radar.js", "", "", 200}} {
		r := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		rec := httptest.NewRecorder()
		api.ServeHTTP(rec, r)
		if rec.Code != tc.status {
			t.Fatalf("%s %s status %d expected %d", tc.method, tc.path, rec.Code, tc.status)
		}
	}
	// A timezone offset must be compared as an instant, not lexically as JSON.
	in := ingest(t, ctx, s, src)
	in.ObservedAt = time.Now().In(time.FixedZone("east", 14*3600))
	o, err := s.Ingest(ctx, in)
	must(t, err)
	changes, err := s.RecentChanges(ctx, u, 1)
	must(t, err)
	found := false
	for _, c := range changes {
		if c.JobID == o.JobID && c.Type == "NEW_JOB" {
			found = true
		}
	}
	if !found {
		t.Fatal("offset observation missing from recent feed")
	}
	raw, err := s.ApplyAction(ctx, u, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, err)
	var app d.Application
	must(t, json.Unmarshal(raw, &app))
	_, err = s.SaveInterview(ctx, u, d.Interview{ApplicationID: app.ID, Round: 1, ScheduledAt: time.Now().Add(time.Hour).In(time.FixedZone("west", -12*3600))})
	must(t, err)
	digest, err := s.DailyDigest(ctx, u)
	must(t, err)
	if digest.Counts["upcoming_interviews"] != 1 {
		t.Fatal("offset interview missing")
	}
}
