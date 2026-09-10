package integration

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"github.com/KanaDoodle/CampusTrace/internal/source"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"github.com/go-sql-driver/mysql"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func statusFor(text string) (string, []d.Claim, error) {
	cs, err := analysis.Extract(context.Background(), text)
	if err != nil {
		return "ERROR", cs, err
	}
	o := d.Observation{ID: "o", PostingID: "p", FetchStatus: "SUCCESS", ExtractionStatus: "COMPLETE", Trust: "OFFICIAL", Text: text, ObservedAt: time.Now()}
	es := []d.Evidence{}
	for _, c := range cs {
		es = append(es, d.Evidence{ObservationID: o.ID, Claim: c})
	}
	return rules.Status("j", []d.Observation{o}, es, time.Now()).Status, cs, nil
}
func TestReleaseSemanticAdversaries(t *testing.T) {
	cases := map[string]string{
		"Applications are open. Applications are closed.":                   "NEEDS_VERIFICATION",
		"Hiring has resumed. Applications are currently paused.":            "NEEDS_VERIFICATION",
		"The old page said 'Apply now'. Applications are currently paused.": "NEEDS_VERIFICATION",
		"Applications are not open. Apply now.":                             "CLOSED", "Applications are currently paused. Apply now.": "CLOSED",
		"暂不开放申请。": "CLOSED", "本岗位尚未开放申请。": "CLOSED", "不要立即申请。": "CLOSED",
		"The old page said 'Apply now'.": "NEEDS_VERIFICATION", "Do not assume applications are closed.": "NEEDS_VERIFICATION",
		"暂停招聘后现已恢复。": "OPEN", "Applications are not closed.": "NEEDS_VERIFICATION", "Applications are no longer closed.": "OPEN",
		"Applications were closed last year, but are open now.": "OPEN", "Hiring has resumed.": "OPEN", "此前已截止，现重新开放申请。": "OPEN", "本岗位仍接受申请。": "OPEN",
	}
	for text, want := range cases {
		state, _, err := statusFor(text)
		must(t, err)
		if state != want {
			t.Errorf("%q: got %s want %s", text, state, want)
		}
	}
}
func TestReleaseDeadlineMatrix(t *testing.T) {
	for _, c := range []struct{ v, z, w string }{{"2026-09-10", "Asia/Shanghai", "2026-09-10T16:00:00Z"}, {"2026-09-10T23:00:00+08:00", "America/New_York", "2026-09-10T15:00:00Z"}, {"2026-09-10T15:00:00Z", "Asia/Shanghai", "2026-09-10T15:00:00Z"}, {"2024-02-29", "Asia/Shanghai", "2024-02-29T16:00:00Z"}, {"2026-03-08", "America/New_York", "2026-03-09T04:00:00Z"}, {"2026-11-01", "America/New_York", "2026-11-02T05:00:00Z"}} {
		got, e := d.DeadlineInstant(c.v, c.z)
		must(t, e)
		if got.UTC().Format(time.RFC3339) != c.w {
			t.Error(c, got)
		}
		t.Log(c, got)
		o := d.Observation{ID: "o", PostingID: "p", Trust: "OFFICIAL", FetchStatus: "SUCCESS", ObservedAt: got.Add(-time.Hour), Timezone: c.z}
		ev := []d.Evidence{{ObservationID: "o", Claim: d.Claim{Type: "DEADLINE", Value: c.v, Confidence: 1}}}
		if rules.Status("j", []d.Observation{o}, ev, got).Status != "CLOSED" {
			t.Error("boundary not exclusive", c)
		}
	}
	for _, c := range []struct{ v, z string }{{"2026-02-29", "UTC"}, {"2026-09-10", "Bogus/Timezone"}} {
		if _, e := d.DeadlineInstant(c.v, c.z); e == nil {
			t.Error("accepted invalid", c)
		}
	}
}
func TestReleaseSourceOverflow(t *testing.T) {
	body := "<p>Apply now</p><script>" + strings.Repeat("x", (1<<20)+50) + "</script><p>Applications are closed.</p>"
	for _, mode := range []string{"plain", "gzip", "wrong-content-type", "redirect"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mode == "redirect" && r.URL.Path != "/target" {
				http.Redirect(w, r, "/target", 302)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			if mode == "wrong-content-type" {
				w.Header().Set("Content-Type", "application/octet-stream")
			}
			if mode == "gzip" {
				w.Header().Set("Content-Encoding", "gzip")
				gz := gzip.NewWriter(w)
				gz.Write([]byte(body))
				gz.Close()
			} else {
				w.Write([]byte(body))
			}
		}))
		res, e := (source.HTTPAdapter{Client: srv.Client()}).Fetch(context.Background(), srv.URL)
		srv.Close()
		must(t, e)
		state, _, _ := statusFor(res.Text)
		t.Logf("mode=%s raw=%d status=%s extracted=%q derived=%s", mode, len(body), res.Status, res.Text, state)
		if res.Status == "SUCCESS" {
			t.Errorf("overflow silently successful: %s", mode)
		}
	}
}

type blockAnalysis struct {
	entered, release chan struct{}
	claim            d.Claim
}

func (a *blockAnalysis) Analyze(ctx context.Context, o d.Observation, _ string) ([]d.Claim, error) {
	close(a.entered)
	select {
	case <-a.release:
		return []d.Claim{a.claim}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestReleaseAnalysisVersionDowngrade(t *testing.T) {
	ctx, s, q, _, src := setup(t)
	in := ingest(t, ctx, s, src)
	in.Text = "degree: BACHELOR\ndegree: MASTER"
	o, e := s.Ingest(ctx, in)
	must(t, e)
	old := worker(s, q)
	old.Version = "model-old"
	a := &blockAnalysis{make(chan struct{}), make(chan struct{}), d.Claim{Type: "EDUCATION_REQUIREMENT", Value: "BACHELOR", Excerpt: "degree: BACHELOR", Method: "RULE", Confidence: 1}}
	old.Analyzer = a
	done := make(chan error, 1)
	go func() { done <- old.Process(ctx, p.NewTask("ANALYZE", o.ID)) }()
	<-a.entered
	newTask, e := s.BindAnalysis(ctx, p.NewTask("ANALYZE", o.ID), d.ProcessingVersion("model-new", d.ParserVersion, o.ParserVersion))
	must(t, e)
	must(t, s.PersistAnalysis(ctx, o.ID, newTask.ProcessingVersion, []d.Claim{{Type: "EDUCATION_REQUIREMENT", Value: "MASTER", Excerpt: "degree: MASTER", Method: "RULE", Confidence: 1}}))
	close(a.release)
	must(t, <-done)
	cur, e := s.Observation(ctx, o.ID)
	must(t, e)
	es, e := s.Evidence(ctx, o.JobID)
	must(t, e)
	_, current := rules.Current([]d.Observation{cur}, es)
	if cur.AnalysisVersion != newTask.ProcessingVersion || cur.CurrentAnalysisGeneration != cur.ActiveAnalysisGeneration {
		t.Fatal("old completion replaced current before receipt replay", cur)
	}
	if len(es) != 2 || len(current) != 1 || current[0].Value != "MASTER" {
		t.Fatal("current/history evidence mixed", es, current)
	}
	next := worker(s, q)
	next.Version = "model-new"
	must(t, next.Process(ctx, p.NewTask("ANALYZE", o.ID)))
	after, e := s.Observation(ctx, o.ID)
	must(t, e)
	t.Logf("current_version=%s current_evidence=%s after_new_worker_retry=%s", cur.AnalysisVersion, d.JSON(current), after.AnalysisVersion)
	if after.AnalysisVersion != newTask.ProcessingVersion {
		t.Error("late old completion downgraded current; new receipt blocks correction")
	}
}
func TestReleasePrepareHistoryAndReceipts(t *testing.T) {
	ctx, s, q, u, src := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", Skills: []string{"go"}, GraduationYear: 2027, PreferredTypes: []string{"FULL_TIME"}}))
	in := ingest(t, ctx, s, src)
	o, e := s.Ingest(ctx, in)
	must(t, e)
	w := worker(s, q)
	must(t, w.Process(ctx, p.NewTask("ANALYZE", o.ID)))
	in.Text = strings.Replace(jd, "BACHELOR", "PHD", 1)
	in.ObservedAt = in.ObservedAt.Add(time.Minute)
	n, e := s.Ingest(ctx, in)
	must(t, e)
	must(t, w.Process(ctx, p.NewTask("ANALYZE", n.ID)))
	tools := &agent.Tools{Store: s, Queue: q, RAG: &rag.Service{Store: s, Sem: make(chan struct{}, 2)}}
	out, e := tools.Prepare(ctx, u, o.JobID)
	must(t, e)
	m := out.(map[string]any)
	reqs := m["current_requirements"].([]d.Evidence)
	oldCount := 0
	for _, ev := range reqs {
		if ev.ObservationID == o.ID {
			oldCount++
		}
	}
	t.Logf("prepare total requirements=%d historical=%d current_eligibility=%s", len(reqs), oldCount, m["eligibility"].(d.Eligibility).Status)
	if oldCount > 0 {
		t.Error("historical evidence exposed as unqualified requirements")
	}
	pending, e := tools.Propose(ctx, u, "create_application", json.RawMessage(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, e)
	first, e := tools.Confirm(ctx, u, pending.ID)
	must(t, e)
	must(t, q.R.Del(ctx, q.Prefix+"pending:"+pending.ID).Err())
	_, e = tools.Confirm(ctx, u, pending.ID)
	var saved string
	must(t, s.DB.QueryRowContext(ctx, "SELECT result FROM action_receipts WHERE id=? AND user_id=?", pending.ID, u).Scan(&saved))
	t.Logf("receipt exists=%v first_bytes=%d re-confirm_error=%v", len(saved) > 0, len(first), e)
	if e != nil {
		t.Error("committed receipt not recoverable without Redis pending")
	}
}
func TestReleaseIdentityEdges(t *testing.T) {
	ctx, s, _, _, src := setup(t)
	in := ingest(t, ctx, s, src)
	in.Text = "same boilerplate"
	in.Locations = []string{"Shanghai", " beijing ", "Shanghai"}
	o, e := s.Ingest(ctx, in)
	must(t, e)
	next := in
	next.SourceID = d.ID()
	must(t, s.SaveSource(ctx, d.Source{ID: next.SourceID, Name: "other official", Trust: "OFFICIAL"}))
	next.ExternalID = "another-source-requisition"
	next.Locations = []string{"beijing", "shanghai"}
	n, e := s.Ingest(ctx, next)
	must(t, e)
	t.Logf("cross-source exact-text normalized-locations merges=%v", n.JobID == o.JobID)
	if n.JobID != o.JobID {
		t.Error("compatible crosssource failed")
	}
	next.ExternalID = "distinct-same-source"
	n2, e := s.Ingest(ctx, next)
	must(t, e)
	if n2.JobID == n.JobID {
		t.Error("same-source distinct requisition merged")
	}
	in.Text = "updated boilerplate"
	in.ObservedAt = in.ObservedAt.Add(time.Minute)
	upd, e := s.Ingest(ctx, in)
	must(t, e)
	if upd.JobID != o.JobID {
		t.Error("stable id changed on content update")
	}
	next.SourceID = d.ID()
	must(t, s.SaveSource(ctx, d.Source{ID: next.SourceID, Name: "third official", Trust: "OFFICIAL"}))
	next.Text = in.Text
	third, e := s.Ingest(ctx, next)
	must(t, e)
	t.Logf("cross-source after content-only update splits=%v", third.JobID != o.JobID)
	t.Logf("unicode NFC/NFD equal=%v whitespace company validation=%v", rules.Fingerprint("Café", "x", "UNKNOWN", nil, "x") == rules.Fingerprint("Cafe\u0301", "x", "UNKNOWN", nil, "x"), (p.Ingest{Company: " ", Title: " ", SourceID: src, Text: "x", JobType: "UNKNOWN", FetchStatus: "SUCCESS"}).Validate())
}

type gateConnector struct {
	driver.Connector
	reached, release chan struct{}
	once             atomic.Bool
}
type gateConn struct {
	driver.Conn
	g *gateConnector
}

func (g *gateConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c, e := g.Connector.Connect(ctx)
	return &gateConn{c, g}, e
}
func (c *gateConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.g.once.CompareAndSwap(false, true) {
		close(c.g.reached)
		select {
		case <-c.g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *gateConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, a)
}
func (c *gateConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, a)
}
func TestReleaseStaleEvaluateInterleaving(t *testing.T) {
	ctx, s, q, u, src := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "ASSOCIATE", Skills: []string{"go"}, GraduationYear: 2027, PreferredTypes: []string{"FULL_TIME"}}))
	in := ingest(t, ctx, s, src)
	o, e := s.Ingest(ctx, in)
	must(t, e)
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", o.ID)))
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		dsn = "campus:local-campus-only@tcp(127.0.0.1:13306)/campustrace_test?parseTime=true"
	}
	cfg, e := mysql.ParseDSN(dsn)
	must(t, e)
	base, e := mysql.NewConnector(cfg)
	must(t, e)
	g := &gateConnector{Connector: base, reached: make(chan struct{}), release: make(chan struct{})}
	db := sql.OpenDB(g)
	defer db.Close()
	stale := &p.Store{DB: db, Weights: s.Weights}
	done := make(chan error, 1)
	snapshot, e := s.ComputeEvaluation(ctx, u, o.JobID)
	must(t, e)
	go func() { done <- stale.PersistAssessment(ctx, snapshot) }()
	<-g.reached
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "MASTER", Skills: []string{"go"}, GraduationYear: 2027, PreferredTypes: []string{"FULL_TIME"}}))
	newE, newR, _, e := s.Evaluate(ctx, u, o.JobID)
	must(t, e)
	close(g.release)
	if err := <-done; !errors.Is(err, p.ErrStaleInput) {
		t.Fatalf("want stale input, got %v", err)
	}
	stored, e := p.One[d.Ranking](ctx, s.DB, "SELECT body FROM rankings WHERE user_id=? AND job_id=?", u, o.JobID)
	must(t, e)
	t.Logf("new_eligibility=%s new_score=%g final_score=%g", newE.Status, newR.Score, stored.Score)
	if stored.Score < newR.Score-1 {
		t.Error("stale Evaluate overwrote newer ranking after profile update")
	}
}
func TestReleaseMCPVisibilityAndGrowth(t *testing.T) {
	ctx, s, q, a, src := setup(t)
	b, e := s.NewUser(ctx, d.ID()+"@reaudit.invalid", "unused")
	must(t, e)
	must(t, s.SaveProfile(ctx, a, d.Profile{}))
	must(t, s.SaveProfile(ctx, b, d.Profile{}))
	in := ingest(t, ctx, s, src)
	in.SourceID = "manual"
	priv, e := s.IngestForUser(ctx, a, in)
	must(t, e)
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", priv.ID)))
	must(t, s.Assess(ctx, d.ID(), priv.JobID))
	in.SourceID = src
	pub, e := s.Ingest(ctx, in)
	must(t, e)
	tools := &agent.Tools{Store: s, Queue: q, RAG: &rag.Service{Store: s, Sem: make(chan struct{}, 2)}}
	au := auth.Service{Store: s, Secret: []byte("reaudit-own-secret-32-characters-long")}
	api := (&transport.API{Store: s, Queue: q, Auth: au, Tools: tools, Metrics: observability.New()}).Handler()
	token, e := au.Token(b)
	must(t, e)
	ev, e := s.Evidence(ctx, priv.JobID)
	must(t, e)
	for _, id := range []string{priv.JobID, priv.PostingID, priv.ID, ev[0].ID, d.ID()} {
		req := httptest.NewRequest("GET", "/api/jobs/"+id, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		api.ServeHTTP(rr, req)
		if rr.Code != 404 {
			t.Errorf("guessed id %s code=%d", id, rr.Code)
		}
	}
	req := httptest.NewRequest("POST", "/api/applications", bytes.NewBufferString(d.JSON(p.CreateArgs{JobID: priv.JobID})))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	api.ServeHTTP(rr, req)
	if rr.Code != 404 {
		t.Error("private create accepted", rr.Code)
	}
	server := transport.MCP(tools, b)
	ct, st := mcp.NewInMemoryTransports()
	ss, e := server.Connect(ctx, st, nil)
	must(t, e)
	defer ss.Close()
	cl := mcp.NewClient(&mcp.Implementation{Name: "reaudit", Version: "1"}, nil)
	cs, e := cl.Connect(ctx, ct, nil)
	must(t, e)
	defer cs.Close()
	for _, name := range []string{"get_job", "get_job_evidence", "get_job_eligibility"} {
		out, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"job_id": priv.JobID}})
		must(t, e)
		if !out.IsError {
			t.Error("MCP private read succeeded", name)
		}
	}
	for _, u := range []string{a, b} {
		if _, e = s.JobForUser(ctx, u, pub.JobID); e != nil {
			t.Error("global hidden", e)
		}
	}
	count := func() int {
		var n int
		must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM eligibilities WHERE user_id=? AND job_id=?", b, pub.JobID).Scan(&n))
		return n
	}
	before := count()
	for i := 0; i < 5; i++ {
		out, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_job_eligibility", Arguments: map[string]any{"job_id": pub.JobID}})
		must(t, e)
		if out.IsError {
			t.Fatal("MCP query failed")
		}
	}
	t.Logf("five MCP queries inserted eligibilities=%d", count()-before)
	if count()-before > 1 {
		t.Error("unchanged MCP reads append duplicate history")
	}
	var polluted int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM rankings WHERE user_id=? AND job_id=?", b, priv.JobID).Scan(&polluted))
	if polluted != 0 {
		t.Error("private background ranking leaked")
	}
}
