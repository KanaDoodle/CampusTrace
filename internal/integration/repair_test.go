package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRepairCanonicalIdentity(t *testing.T) {
	for _, mode := range []string{"company-boundary", "external-case", "url-case", "candidate-city", "candidate-type", "candidate-bu", "candidate-bu-text"} {
		t.Run(mode, func(t *testing.T) {
			ctx, s, _, _, src := setup(t)
			a := ingest(t, ctx, s, src)
			b := a
			b.ExternalID = d.ID()
			a.Text = "tech: go"
			b.Text = a.Text
			switch mode {
			case "company-boundary":
				suffix := d.ID()
				a.Company = "North Labs " + suffix
				a.Title = "Backend"
				b.Company = "North"
				b.Title = "Labs " + suffix + " Backend"
			case "external-case":
				a.ExternalID = "CaseA"
				b.ExternalID = "casea"
				b.Locations = []string{"Beijing"}
			case "url-case":
				a.ExternalID = ""
				b.ExternalID = ""
				a.URL = "https://example.test/Jobs/ABC"
				b.URL = "https://example.test/jobs/abc"
				b.JobType = "INTERNSHIP"
			case "candidate-city":
				b.Locations = []string{"Beijing"}
			case "candidate-type":
				b.JobType = "INTERNSHIP"
			case "candidate-bu":
				b.Title = "Payments BU backend"
			case "candidate-bu-text":
				b.Text = "tech: go\nbu: Payments"
			}
			first, err := s.Ingest(ctx, a)
			must(t, err)
			if strings.HasPrefix(mode, "candidate-") {
				b.SourceID = d.ID()
				must(t, s.SaveSource(ctx, d.Source{ID: b.SourceID, Name: "independent source", Type: "OFFICIAL", Trust: "OFFICIAL"}))
				_, err = s.DB.ExecContext(ctx, "UPDATE jobs SET fingerprint=? WHERE id=?", rules.Fingerprint(b.Company, b.Title, b.JobType, b.Locations, b.Text), first.JobID)
				must(t, err)
			}
			second, err := s.Ingest(ctx, b)
			must(t, err)
			if first.JobID == second.JobID {
				t.Fatal("F02: incompatible posting merged", mode)
			}
		})
	}
}
func TestRepairColdAndCachedClaims(t *testing.T) {
	ctx, s, q, _, src := setup(t)
	w := worker(s, q)
	a := ingest(t, ctx, s, src)
	a.Text = "tech: go\napply: PRESENT"
	o, err := s.Ingest(ctx, a)
	must(t, err)
	must(t, w.Process(ctx, p.NewTask("ANALYZE", o.ID)))
	for _, text := range []string{a.Text, "tech: go apply: PRESENT"} {
		var coldClaims []d.Claim
		var coldStatus string
		for n := 0; n < 2; n++ {
			in := a
			in.Company = "cache " + d.ID()
			in.ExternalID = d.ID()
			in.Text = text
			if n == 0 {
				keys, _ := q.R.Keys(ctx, q.Prefix+"analysis:*").Result()
				if len(keys) > 0 {
					q.R.Del(ctx, keys...)
				}
			}
			// Seed the colliding input before the second text's hot path.
			if n == 1 && text != a.Text {
				keys, _ := q.R.Keys(ctx, q.Prefix+"analysis:*").Result()
				if len(keys) > 0 {
					q.R.Del(ctx, keys...)
				}
				seed := a
				seed.Company = "seed " + d.ID()
				seed.ExternalID = d.ID()
				so, e := s.Ingest(ctx, seed)
				must(t, e)
				must(t, w.Process(ctx, p.NewTask("ANALYZE", so.ID)))
			}
			oo, e := s.Ingest(ctx, in)
			must(t, e)
			must(t, w.Process(ctx, p.NewTask("ANALYZE", oo.ID)))
			es, e := s.Evidence(ctx, oo.JobID)
			must(t, e)
			cs := []d.Claim{}
			for _, v := range es {
				cs = append(cs, v.Claim)
			}
			sortClaims := func(v []d.Claim) map[string]d.Claim {
				m := map[string]d.Claim{}
				for _, c := range v {
					m[c.Type+":"+c.Value] = c
				}
				return m
			}
			as := rules.Status(oo.JobID, []d.Observation{oo}, es, time.Now()).Status
			if n == 0 {
				coldClaims = cs
				coldStatus = as
			} else if !reflect.DeepEqual(sortClaims(coldClaims), sortClaims(cs)) || coldStatus != as {
				t.Fatalf("F03: cold=%v %s hot=%v %s", coldClaims, coldStatus, cs, as)
			}
		}
	}
}
func TestRepairManualOwnership(t *testing.T) {
	ctx, s, q, a, src := setup(t)
	b, err := s.NewUser(ctx, d.ID()+"@private.test", "unused")
	must(t, err)
	must(t, s.SaveProfile(ctx, a, d.Profile{}))
	must(t, s.SaveProfile(ctx, b, d.Profile{}))
	au := auth.Service{Store: s, Secret: []byte("repair-ownership-secret-at-least-32")}
	tools := &agent.Tools{Store: s, Queue: q, RAG: &rag.Service{Store: s, Sem: make(chan struct{}, 2)}}
	api := (&transport.API{Store: s, Queue: q, Auth: au, Tools: tools, Metrics: observability.New()}).Handler()
	// Old installations already contain the shared manual source.
	var n int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM sources WHERE id='manual'").Scan(&n))
	if n == 0 {
		must(t, s.SaveSource(ctx, d.Source{ID: "manual", Name: "legacy manual", Type: "MANUAL", Trust: "MANUAL"}))
	}
	call := func(user, method, url string, body any) *httptest.ResponseRecorder {
		token, e := au.Token(user)
		must(t, e)
		req := httptest.NewRequest(method, url, bytes.NewBufferString(d.JSON(body)))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		api.ServeHTTP(rr, req)
		return rr
	}
	in := ingest(t, ctx, s, src)
	in.Text = "Private original " + d.ID()
	r := call(a, "POST", "/api/ingest", in)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var first d.Observation
	must(t, json.Unmarshal(r.Body.Bytes(), &first))
	if r = call(b, "GET", "/api/jobs/"+first.JobID, struct{}{}); r.Code != http.StatusNotFound {
		t.Errorf("F06: B read A private detail: %d", r.Code)
	}
	if r = call(a, "GET", "/api/jobs/"+first.JobID, struct{}{}); r.Code != 200 {
		t.Errorf("owner denied: %d", r.Code)
	}
	r = call(b, "POST", "/api/ingest", in)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	var second d.Observation
	must(t, json.Unmarshal(r.Body.Bytes(), &second))
	if first.PostingID == second.PostingID || first.JobID == second.JobID {
		t.Error("F06: cross-user manual pollution")
	}
	post, e := p.One[d.Posting](ctx, s.DB, "SELECT body FROM postings WHERE id=?", first.PostingID)
	must(t, e)
	attack := in
	attack.SourceID = post.SourceID
	if _, e = s.IngestForUser(ctx, b, attack); e == nil {
		t.Error("F06: B appended to A private source by ID")
	}
	own, e := s.IngestForUser(ctx, a, attack)
	must(t, e)
	if own.PostingID != first.PostingID {
		t.Fatal("owner lost stable manual posting")
	}
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", own.ID)))
	must(t, s.Assess(ctx, d.ID(), own.JobID))
	var polluted int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM rankings WHERE job_id=? AND user_id=?", first.JobID, b).Scan(&polluted))
	if polluted != 0 {
		t.Error("F06: private evidence reached another user's ranking")
	}
	list, e := s.JobsForUser(ctx, b, in.Company)
	must(t, e)
	for _, j := range list {
		if j.ID == first.JobID {
			t.Error("F06: private job appeared in B catalog")
		}
	}

	for _, name := range []string{"get_job", "get_job_evidence", "get_job_eligibility", "get_preparation_context"} {
		if _, e := tools.Execute(ctx, b, name, json.RawMessage(fmt.Sprintf(`{"job_id":%q}`, first.JobID))); e == nil {
			t.Errorf("F06: agent IDOR %s", name)
		}
	}
	if _, e := s.ApplyAction(ctx, b, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: first.JobID}))); e == nil {
		t.Error("F06: private job application IDOR")
	}
	pub, e := s.Ingest(ctx, in)
	if e == nil && (pub.JobID == first.JobID || pub.JobID == second.JobID) {
		t.Error("F06: manual polluted shared catalog")
	}
	must(t, e)
	for _, u := range []string{a, b} {
		if r = call(u, "GET", "/api/jobs/"+pub.JobID, struct{}{}); r.Code != 200 {
			t.Errorf("global source unreadable %d", r.Code)
		}
	}
}

func TestRepairMetadataRefresh(t *testing.T) {
	ctx, s, q, u, src := setup(t)
	in := ingest(t, ctx, s, src)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	in.Title = "Payments Backend"
	in.JobType = "INTERNSHIP"
	in.Locations = []string{"Beijing"}
	in.ObservedAt = in.ObservedAt.Add(time.Minute)
	next, err := s.Ingest(ctx, in)
	must(t, err)
	if o.JobID != next.JobID {
		t.Fatal("stable source posting lost")
	}
	j, err := s.Job(ctx, next.JobID)
	must(t, err)
	if j.Title != in.Title || j.JobType != in.JobType || !reflect.DeepEqual(j.Locations, in.Locations) {
		t.Fatal("ranking metadata stale", j)
	}
	must(t, s.SaveProfile(ctx, u, d.Profile{PreferredCities: []string{"Beijing"}, PreferredTypes: []string{"INTERNSHIP"}, TargetRoles: []string{"Payments"}}))
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", next.ID)))
	_, rank, _, err := s.Evaluate(ctx, u, next.JobID)
	must(t, err)
	if rank.Breakdown["city"] != 10 || rank.Breakdown["type"] != 5 || rank.Breakdown["role"] != 10 {
		t.Fatal(rank)
	}
	for _, key := range []string{"city", "type", "role"} {
		if rank.BreakdownSources[key] != "JOB_METADATA_AND_USER_PREFERENCE" {
			t.Fatal("unlabelled metadata score")
		}
	}
	if rank.BreakdownSources["eligibility"] != "EVIDENCE_AND_USER_PROFILE" {
		t.Fatal("missing evidence provenance")
	}
	// A late older observation must not roll current metadata backward.
	in.ObservedAt = in.ObservedAt.Add(-2 * time.Minute)
	in.Title = "old"
	_, err = s.Ingest(ctx, in)
	must(t, err)
	j, err = s.Job(ctx, next.JobID)
	must(t, err)
	if j.Title == "old" {
		t.Fatal("older metadata overwrote current")
	}
}
func TestRepairSourceTimezone(t *testing.T) {
	ctx, s, _, _, _ := setup(t)
	src := d.ID()
	must(t, s.SaveSource(ctx, d.Source{ID: src, Name: "US source", Type: "OFFICIAL", Trust: "OFFICIAL", Timezone: "America/New_York"}))
	in := ingest(t, ctx, s, src)
	in.Text = "deadline: 2026-09-30"
	o, err := s.Ingest(ctx, in)
	must(t, err)
	if o.Timezone != "America/New_York" {
		t.Fatal("source timezone not propagated")
	}
	deadline, err := d.DeadlineInstant("2026-09-30", o.Timezone)
	must(t, err)
	if deadline.UTC().Format(time.RFC3339) != "2026-10-01T04:00:00Z" {
		t.Fatal(deadline)
	}
}

func TestRepairLegacyCacheAndReceiptInvalidation(t *testing.T) {
	ctx, s, q, _, src := setup(t)
	in := ingest(t, ctx, s, src)
	in.Text = "tech: go apply: PRESENT"
	o, err := s.Ingest(ctx, in)
	must(t, err)
	// Model the audit's already persisted v1 hot-cache result, without asking the
	// new semantic validator to bless historical corruption.
	old := d.Evidence{ID: d.ID(), ObservationID: o.ID, JobID: o.JobID, Claim: d.Claim{Type: "APPLY_ACTION", Value: "PRESENT", Excerpt: "apply: PRESENT", Method: "RULE", Confidence: 1}}
	_, err = s.DB.ExecContext(ctx, "INSERT INTO evidence(id,observation_id,job_id,body) VALUES(?,?,?,?)", old.ID, o.ID, o.JobID, d.JSON(old))
	must(t, err)
	_, err = s.DB.ExecContext(ctx, "INSERT INTO analysis_results(observation_id,analysis_version,created_at) VALUES(?,'claims-v1',?)", o.ID, time.Now().UTC())
	must(t, err)
	must(t, q.R.Set(ctx, q.Prefix+"analysis:claims-v1:"+o.Hash, d.JSON([]d.Claim{old.Claim}), time.Hour).Err())
	w := worker(s, q)
	w.Version = "claims-v1"
	must(t, w.Process(ctx, p.NewTask("ANALYZE", o.ID)))
	current, err := s.Observation(ctx, o.ID)
	must(t, err)
	es, err := s.Evidence(ctx, o.JobID)
	must(t, err)
	_, selected := rules.Current([]d.Observation{current}, es)
	if len(selected) != 1 || selected[0].Type != "TECH_STACK" {
		t.Fatalf("F03: legacy receipt/cache/evidence still active: %+v", selected)
	}
	if a := rules.Status(o.JobID, []d.Observation{current}, es, time.Now()); a.Status == "OPEN" {
		t.Fatal("F03: legacy cache business error survived reanalysis")
	}
}
