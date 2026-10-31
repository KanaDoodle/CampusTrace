package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"github.com/KanaDoodle/CampusTrace/internal/searchservice"
)

const searchQueryToken = "integration-search-query-01234567890123456789"
const searchWriteToken = "integration-search-write-01234567890123456789"

func searchFixture(t *testing.T, s *p.Store) *a.Client {
	t.Helper()
	endpoint := os.Getenv("OPENSEARCH_TEST_URL")
	if endpoint == "" {
		t.Skip("requires disposable OpenSearch")
	}
	alias := "test-core-" + d.ID()
	backend, err := searchservice.NewBackend(endpoint, alias)
	must(t, err)
	server, err := searchservice.NewServer(backend, searchQueryToken, searchWriteToken)
	must(t, err)
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	t.Cleanup(func() {
		request, _ := http.NewRequest("DELETE", endpoint+"/"+alias+"-*", nil)
		if response, e := httpServer.Client().Do(request); e == nil {
			response.Body.Close()
		}
	})
	client, err := a.NewClient(httpServer.URL, searchQueryToken, searchWriteToken)
	must(t, err)
	s.Search = client
	return client
}
func drainSearch(t *testing.T, ctx context.Context, s *p.Store, user, word string) {
	t.Helper()
	for i := 0; i < 30; i++ {
		must(t, s.SyncSearchOnce(ctx))
		out, e := s.SearchJobs(ctx, user, a.Query{Text: word, Limit: 100})
		must(t, e)
		if out.Mode == "fulltext" {
			return
		}
	}
	t.Fatal("search projection did not catch up")
}
func TestSearchProjectionIsolationRecovery(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable OpenSearch")
	}
	ctx, s, _, alice, source := setup(t)
	client := searchFixture(t, s)
	bob, e := s.NewUser(ctx, d.ID()+"@search.invalid", "unused")
	must(t, e)
	in := ingest(t, ctx, s, source)
	in.Title = "岗位甲"
	in.Text = "C++ Redis 分布式服务器开发"
	global, e := s.Ingest(ctx, in)
	must(t, e)
	privateInput := in
	privateInput.SourceID = "manual"
	privateInput.Title = "岗位乙"
	privateInput.Text = "Golang MySQL 私有岗位"
	private, e := s.IngestForUser(ctx, alice, privateInput)
	must(t, e)
	before, e := s.SearchJobs(ctx, alice, a.Query{Company: in.Company, Text: "Go"})
	must(t, e)
	if before.Mode != "basic" || len(before.IDs) != 1 || before.IDs[0] != private.JobID {
		t.Fatal("pre-backfill fallback", before)
	}
	drainSearch(t, ctx, s, alice, "Go")
	for _, tc := range []struct {
		user, query string
		count       int
	}{{alice, "Go", 1}, {bob, "Go", 0}, {bob, "C++", 1}, {alice, "分布式", 1}} {
		out, e := s.SearchJobs(ctx, tc.user, a.Query{Company: in.Company, Text: tc.query})
		must(t, e)
		if out.Mode != "fulltext" || len(out.IDs) != tc.count {
			t.Fatalf("%s %q %+v", tc.user, tc.query, out)
		}
	}
	tools := agent.Tools{Store: s}
	raw, e := tools.Execute(ctx, alice, "search_jobs", []byte(`{"query":"Go"}`))
	must(t, e)
	if !strings.Contains(d.JSON(raw), private.JobID) {
		t.Fatal("Agent bypassed search", d.JSON(raw))
	}
	// Search failure leaves Core writes and JD fallback available.
	offline, _ := a.NewClient("http://127.0.0.1:1", searchQueryToken, searchWriteToken)
	s.Search = offline
	in.ObservedAt = in.ObservedAt.Add(time.Second)
	in.Text = "Rust Tokio 更新后岗位"
	_, e = s.Ingest(ctx, in)
	must(t, e)
	out, e := s.SearchJobs(ctx, bob, a.Query{Company: in.Company, Text: "Tokio"})
	must(t, e)
	if out.Mode != "basic" || len(out.IDs) != 1 {
		t.Fatal("outage fallback", out)
	}
	if s.SyncSearchOnce(ctx) == nil {
		t.Fatal("outage silently acknowledged")
	}
	s.Search = client
	drainSearch(t, ctx, s, alice, "Tokio")
	out, e = s.SearchJobs(ctx, alice, a.Query{Company: in.Company, Text: "C++"})
	must(t, e)
	if len(out.IDs) != 0 {
		t.Fatal("old JD still searchable", out)
	}
	// A visibility mutation outside the application bypasses the journal. Core's
	// final ACL/hash check must still suppress a stale hit immediately.
	_, e = s.DB.ExecContext(ctx, "UPDATE jobs SET visibility='PRIVATE',owner_id=? WHERE id=?", alice, global.JobID)
	must(t, e)
	out, e = s.SearchJobs(ctx, bob, a.Query{Company: in.Company, Text: "Tokio"})
	must(t, e)
	if out.Mode != "basic" || len(out.IDs) != 0 {
		t.Fatal("stale ACL leaked", out)
	}
	_, e = s.DB.ExecContext(ctx, "UPDATE search_revision_state SET next_reconcile_at=NULL WHERE id='jobs'")
	must(t, e)
	drainSearch(t, ctx, s, alice, "Tokio")
	out, e = s.SearchJobs(ctx, bob, a.Query{Company: in.Company, Text: "Tokio"})
	must(t, e)
	if out.Mode != "fulltext" || len(out.IDs) != 0 {
		t.Fatal("ACL not synchronized", out)
	}
	// Online preparation keeps the old generation serving complete reads.
	_, e = client.Rebuild(ctx)
	must(t, e)
	out, e = s.SearchJobs(ctx, alice, a.Query{Company: in.Company, Text: "Go"})
	must(t, e)
	if out.Mode != "fulltext" || len(out.IDs) != 1 {
		t.Fatal("online rebuild interrupted reads", out)
	}
	finishSearchRebuild(t, ctx, s, client)
	drainSearch(t, ctx, s, alice, "Go")
}

type replayTransport struct {
	a.Transport
	after func()
	fail  bool
}

func (r *replayTransport) Apply(ctx context.Context, b a.Batch) error {
	if e := r.Transport.Apply(ctx, b); e != nil {
		return e
	}
	if r.after != nil {
		f := r.after
		r.after = nil
		f()
	}
	if r.fail {
		r.fail = false
		return a.ErrUnavailable
	}
	return nil
}
func TestSearchAcknowledgementAndTombstone(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable OpenSearch")
	}
	ctx, s, _, user, source := setup(t)
	client := searchFixture(t, s)
	in := ingest(t, ctx, s, source)
	in.Text = "Golang 旧内容"
	job, e := s.Ingest(ctx, in)
	must(t, e)
	drainSearch(t, ctx, s, user, "Go")
	in.Text = "Golang 中间内容"
	in.ObservedAt = in.ObservedAt.Add(time.Second)
	_, e = s.Ingest(ctx, in)
	must(t, e)
	replay := &replayTransport{Transport: client, fail: true}
	s.Search = replay
	if e = s.SyncSearchOnce(ctx); !errors.Is(e, a.ErrUnavailable) {
		t.Fatal("failure injection", e)
	}
	var pending int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_outbox WHERE job_id=?", job.JobID).Scan(&pending))
	if pending != 1 {
		t.Fatal("acknowledged failed delivery")
	}
	replay.after = func() {
		in.Text = "Golang 最新内容"
		in.ObservedAt = in.ObservedAt.Add(time.Second)
		_, e = s.Ingest(ctx, in)
		must(t, e)
	}
	must(t, s.SyncSearchOnce(ctx))
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_outbox WHERE job_id=?", job.JobID).Scan(&pending))
	if pending != 1 {
		t.Fatal("older ack removed newer version")
	}
	s.Search = client
	drainSearch(t, ctx, s, user, "最新")
	old, e := p.One[a.Document](ctx, s.DB, "SELECT body FROM search_documents WHERE job_id=?", job.JobID)
	must(t, e)
	// Simulate an operator deletion after removing owned dependent business rows.
	must(t, s.Tx(ctx, func(tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, "DELETE FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (SELECT id FROM observations WHERE job_id=?) OR JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id'))=?", job.JobID, job.JobID); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM agent_events WHERE job_id=?", job.JobID); e != nil {
			return e
		}
		for _, statement := range []string{"DELETE FROM job_assessment_schedule WHERE job_id=?", "DELETE FROM observations WHERE job_id=?", "DELETE FROM postings WHERE job_id=?", "DELETE FROM jobs WHERE id=?"} {
			if _, e := tx.ExecContext(ctx, statement, job.JobID); e != nil {
				return e
			}
		}
		return nil
	}))
	out, e := s.SearchJobs(ctx, user, a.Query{Company: in.Company, Text: "Go"})
	must(t, e)
	if len(out.IDs) != 0 || out.Mode != "basic" {
		t.Fatal("deleted stale hit leaked", out)
	}
	_, e = s.DB.ExecContext(ctx, "UPDATE search_revision_state SET next_reconcile_at=NULL WHERE id='jobs'")
	must(t, e)
	drainSearch(t, ctx, s, user, "Go")
	tombstone, e := p.One[a.Document](ctx, s.DB, "SELECT body FROM search_documents WHERE job_id=?", job.JobID)
	must(t, e)
	if !tombstone.Deleted || tombstone.Version <= old.Version || tombstone.Text != "" {
		t.Fatal("missing versioned tombstone", tombstone)
	}
	status, e := client.Status(ctx)
	must(t, e)
	must(t, client.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: status.Generation, Documents: []a.Document{old}}))
	out, e = s.SearchJobs(ctx, user, a.Query{Company: in.Company, Text: "Go"})
	must(t, e)
	if len(out.IDs) != 0 {
		t.Fatal("delayed update resurrected deletion")
	}
	// No job owns this orphaned tombstone after the fixture ends.
	t.Cleanup(func() {
		s.DB.Exec("DELETE FROM search_documents WHERE job_id=?", job.JobID)
		s.DB.Exec("DELETE FROM search_outbox WHERE job_id=?", job.JobID)
	})
}

func TestSearchInitialBackfillAcrossPages(t *testing.T) {
	if os.Getenv("OPENSEARCH_TEST_URL") == "" {
		t.Skip("requires disposable OpenSearch")
	}
	ctx, s, _, user, source := setup(t)
	searchFixture(t, s)
	in := ingest(t, ctx, s, source)
	for i := 0; i < 105; i++ {
		in.Title = fmt.Sprintf("历史岗位 %03d", i)
		in.ExternalID = d.ID()
		_, err := s.Ingest(ctx, in)
		must(t, err)
	}
	// Simulate pre-migration jobs that never had projection rows or journal entries.
	for _, table := range []string{"search_outbox", "search_documents"} {
		_, err := s.DB.ExecContext(ctx, "DELETE FROM "+table+" WHERE job_id IN (SELECT job_id FROM postings WHERE source_id=?)", source)
		must(t, err)
	}
	_, err := s.DB.ExecContext(ctx, "UPDATE search_revision_state SET initial_done=FALSE,reconcile_cursor='',next_reconcile_at=NULL WHERE id='jobs'")
	must(t, err)
	must(t, s.SyncSearchOnce(ctx))
	out, err := s.SearchJobs(ctx, user, a.Query{Company: in.Company, Text: "历史岗位", Limit: 1000})
	must(t, err)
	if out.Mode != "basic" || len(out.IDs) != 105 {
		t.Fatal("partial backfill escaped readiness guard", out.Mode, len(out.IDs))
	}
	drainSearch(t, ctx, s, user, "历史岗位")
	out, err = s.SearchJobs(ctx, user, a.Query{Company: in.Company, Text: "历史岗位", Limit: 1000})
	must(t, err)
	if out.Mode != "fulltext" || len(out.IDs) != 105 || out.More {
		t.Fatal("backfill lost jobs", out.Mode, len(out.IDs), out.More)
	}
	out, err = s.SearchJobs(ctx, user, a.Query{Company: in.Company, Text: "历史岗位", Limit: 1})
	must(t, err)
	if len(out.IDs) != 1 || !out.More {
		t.Fatal("verified continuation missing", out)
	}
}

func TestSearchHTTPAuthoritativeIdentity(t *testing.T) {
	ctx, s, q, alice, _ := setup(t)
	bob, err := s.NewUser(ctx, d.ID()+"@search-http.invalid", "unused")
	must(t, err)
	in := p.Ingest{SourceID: "manual", Company: "Private Search " + d.ID(), Title: "服务器岗位", JobType: "FULL_TIME", Locations: []string{"上海"}, Text: "Golang Redis", FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC()}
	job, err := s.IngestForUser(ctx, alice, in)
	must(t, err)
	authn := auth.Service{Store: s, Secret: []byte("synthetic-search-http-auth-secret-32")}
	aliceToken, err := authn.Token(alice)
	must(t, err)
	bobToken, err := authn.Token(bob)
	must(t, err)
	handler := (&transport.API{Store: s, Queue: q, Auth: authn, Metrics: observability.New()}).Handler()
	for _, tc := range []struct {
		token, body   string
		status, count int
	}{{aliceToken, `{"text":"Redis"}`, 200, 1}, {bobToken, `{"text":"Redis","company":"` + in.Company + `"}`, 200, 0}, {"", `{"text":"Redis"}`, 401, 0}, {bobToken, `{"text":"Redis","user":"` + alice + `"}`, 400, 0}, {aliceToken, `{"limit":1001}`, 400, 0}, {aliceToken, `{"mode":"BAD"}`, 400, 0}} {
		r := httptest.NewRequest("POST", "/api/jobs/search", strings.NewReader(tc.body))
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("HTTP search %s: %d %s", tc.body, w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var out p.JobSearch
			must(t, json.Unmarshal(w.Body.Bytes(), &out))
			if tc.token == aliceToken {
				found := false
				for _, id := range out.IDs {
					if id == job.JobID {
						found = true
					}
				}
				if !found {
					t.Fatal("private JD search missing")
				}
			} else if len(out.IDs) != tc.count {
				t.Fatal("other account saw private jobs", out.IDs)
			}
		}
	}
}

func finishSearchRebuild(t *testing.T, ctx context.Context, s *p.Store, client *a.Client) {
	t.Helper()
	for i := 0; i < 80; i++ {
		e := s.SyncSearchOnce(ctx)
		if e != nil && !errors.Is(e, a.ErrPending) && !errors.Is(e, a.ErrConflict) {
			t.Fatal(e)
		}
		status, e := client.Status(ctx)
		must(t, e)
		if status.Rebuild != nil && status.Rebuild.Phase == "DONE" {
			return
		}
	}
	t.Fatal("online rebuild did not finish")
}
