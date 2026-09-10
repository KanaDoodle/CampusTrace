package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/redis/go-redis/v9"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func setup(t *testing.T) (context.Context, *p.Store, *pipeline.Queue, string, string) {
	t.Helper()
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("set CAMPUS_INTEGRATION=1; requires compose MySQL, Redis, etcd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	c := config.Load()
	s, err := p.Open(ctx, config.Env("MYSQL_TEST_DSN", strings.Replace(c.DSN, "/campustrace?", "/campustrace_test?", 1)))
	must(t, err)
	t.Cleanup(func() { s.DB.Close() })
	must(t, s.Migrate(ctx))
	r := redis.NewClient(&redis.Options{Addr: c.Redis, ContextTimeoutEnabled: true})
	must(t, r.Ping(ctx).Err())
	q := &pipeline.Queue{R: r, Prefix: "test:" + d.ID() + ":"}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		keys, _ := r.Keys(cleanup, q.Prefix+"*").Result()
		if len(keys) > 0 {
			r.Del(cleanup, keys...)
		}
		r.Close()
	})
	u, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused-test-hash")
	must(t, err)
	source := d.ID()
	must(t, s.SaveSource(ctx, d.Source{ID: source, Name: "Synthetic integration official", Type: "OFFICIAL", Trust: "OFFICIAL"}))
	return ctx, s, q, u, source
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

const jd = "graduation: 2027\ndegree: BACHELOR\njob_type: FULL_TIME\nlocation: Shanghai\nexperience_months: 0\ntech: REQUIRED:go\napply: PRESENT"

func ingest(t *testing.T, ctx context.Context, s *p.Store, source string) p.Ingest {
	t.Helper()
	return p.Ingest{Company: "Synthetic Integration " + d.ID(), Title: "Go backend", JobType: "FULL_TIME", Locations: []string{"Shanghai"}, SourceID: source, ExternalID: d.ID(), Text: jd, FetchStatus: "SUCCESS", ObservedAt: time.Now().UTC().Add(-time.Hour)}
}

type localAnalyzer struct{}

func (localAnalyzer) Analyze(ctx context.Context, o d.Observation, _ string) ([]d.Claim, error) {
	return analysis.Extract(ctx, o.Text)
}
func worker(s *p.Store, q *pipeline.Queue) *pipeline.Worker {
	return &pipeline.Worker{Store: s, Queue: q, Analyzer: localAnalyzer{}, Concurrency: 2, MaxAttempts: 2, Timeout: time.Second, ClaimIdle: 3 * time.Second, Metrics: observability.New()}
}
func TestBusinessPaths(t *testing.T) {
	ctx, s, q, user, src := setup(t)
	in := ingest(t, ctx, s, src)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	w := worker(s, q)
	task := p.NewTask("ANALYZE", o.ID)
	must(t, w.Process(ctx, task))
	must(t, s.Assess(ctx, d.ID(), o.JobID))
	j, err := s.Job(ctx, o.JobID)
	must(t, err)
	if j.CurrentStatus != "OPEN" {
		t.Fatal(j)
	}
	es, err := s.Evidence(ctx, j.ID)
	must(t, err)
	if len(es) == 0 || es[0].ObservationID != o.ID {
		t.Fatal("evidence binding")
	}
	as, err := s.Assessments(ctx, j.ID)
	must(t, err)
	if len(as) != 1 || len(as[0].EvidenceIDs) == 0 {
		t.Fatal(as)
	}
	in.Text = jd + "\ndeadline: 2020-01-01"
	in.ObservedAt = in.ObservedAt.Add(time.Minute)
	o2, err := s.Ingest(ctx, in)
	must(t, err)
	if o2.JobID != j.ID {
		t.Fatal("external identity not preserved")
	}
	must(t, w.Process(ctx, p.NewTask("ANALYZE", o2.ID)))
	must(t, s.Assess(ctx, d.ID(), j.ID))
	changes, err := s.Changes(ctx, j.ID)
	must(t, err)
	if len(changes) != 2 {
		t.Fatalf("changes %+v", changes)
	}
	j, err = s.Job(ctx, j.ID)
	must(t, err)
	if j.CurrentStatus != "CLOSED" {
		t.Fatal(j)
	}
	// Exact normalized content can merge across source identities, URL is not job identity.
	other := d.ID()
	must(t, s.SaveSource(ctx, d.Source{ID: other, Name: "third", Type: "THIRD_PARTY", Trust: "THIRD_PARTY"}))
	in.SourceID = other
	in.ExternalID = d.ID()
	in.Text = jd
	merged, err := s.Ingest(ctx, in)
	must(t, err)
	if merged.JobID != j.ID {
		t.Fatal("exact canonical merge failed")
	}
	profile := d.Profile{GraduationYear: 2027, Degree: "BACHELOR", PreferredTypes: []string{"FULL_TIME"}, PreferredCities: []string{"Shanghai"}, Skills: []string{"go"}}
	must(t, s.SaveProfile(ctx, user, profile))
	e, rank, fit, err := s.Evaluate(ctx, user, j.ID)
	must(t, err)
	if e.Status != "ELIGIBLE" || fit != "EXPLICIT_GO" || rank.Score <= 0 {
		t.Fatal(e, rank, fit)
	}
	raw, err := s.ApplyAction(ctx, user, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: j.ID})))
	must(t, err)
	var app d.Application
	must(t, json.Unmarshal(raw, &app))
	raw, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, Version: 1, State: "APPLIED"})))
	must(t, err)
	_, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, Version: 1, State: "OA"})))
	if !errors.Is(err, p.ErrConflict) {
		t.Fatal("stale version accepted", err)
	}
	_, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, Version: 2, State: "PLANNED"})))
	if err == nil {
		t.Fatal("invalid transition accepted")
	}
	history, err := s.ApplicationHistory(ctx, user, app.ID)
	must(t, err)
	if len(history) != 2 {
		t.Fatal(history)
	}
	rg := &rag.Service{Store: s, Sem: make(chan struct{}, 2)}
	tools := &agent.Tools{Store: s, Queue: q, RAG: rg}
	for i := 0; i < 2; i++ {
		v, err := s.SaveInterview(ctx, user, d.Interview{ApplicationID: app.ID, Round: i + 1, ScheduledAt: time.Now(), Result: "PENDING"})
		must(t, err)
		review := d.Review{InterviewID: v.ID, Questions: []string{"Explain PEL recovery"}, Evaluation: "Missed PEL recovery", Missed: []string{"PEL recovery"}, Topics: []d.WeakCandidate{{Topic: "Redis Streams", Weight: 3, Evidence: "PEL recovery"}}}
		_, err = s.ApplyAction(ctx, user, d.ID(), "record_interview_review", []byte(d.JSON(review)))
		must(t, err)
	}
	weak, err := p.Many[d.WeakTopic](ctx, s.DB, "SELECT body FROM weak_topics WHERE user_id=?", user)
	must(t, err)
	if len(weak) != 1 || weak[0].Count != 2 {
		t.Fatal(weak)
	}
	_, err = rg.Ingest(ctx, user, rag.Document{Title: "Redis Streams", Text: "PEL recovery uses XAUTOCLAIM. ACK after durable transaction commit."})
	must(t, err)
	hits, err := rg.Search(ctx, user, "Redis PEL", 3)
	must(t, err)
	if len(hits) != 1 || !strings.Contains(hits[0].Text, "XAUTOCLAIM") {
		t.Fatal(hits)
	}
	prep, err := tools.Prepare(ctx, user, j.ID)
	must(t, err)
	if !strings.Contains(d.JSON(prep), "redis streams") {
		t.Fatal("feedback absent")
	}
	t.Log("A-F, M: observation/evidence/status; change/reassessment; eligibility/ranking; optimistic workflow; weak-topic feedback; RAG passed")
}
func readOne(t *testing.T, ctx context.Context, q *pipeline.Queue, consumer string) redis.XMessage {
	t.Helper()
	streams, err := q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: q.Group(), Consumer: consumer, Streams: []string{q.Stream(), ">"}, Count: 1, Block: time.Second}).Result()
	must(t, err)
	return streams[0].Messages[0]
}
func TestRedisReliability(t *testing.T) {
	ctx, s, q, _, src := setup(t)
	must(t, q.Init(ctx))
	in := ingest(t, ctx, s, src)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	w := worker(s, q)
	task := p.NewTask("ANALYZE", o.ID)
	must(t, q.Publish(ctx, task))
	m := readOne(t, ctx, q, "crashed-worker")
	decoded, err := pipeline.Decode(m)
	must(t, err)
	must(t, w.Process(ctx, decoded))
	// Crash point: SQL committed, no ACK. Claim and redeliver the same task.
	time.Sleep(15 * time.Millisecond)
	recovered, err := q.Recover(ctx, "replacement", time.Millisecond)
	must(t, err)
	if len(recovered) != 1 {
		t.Fatal("PEL not recovered")
	}
	must(t, w.Process(ctx, decoded))
	must(t, q.Ack(ctx, m.ID))
	var n int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM analysis_results WHERE observation_id=?", o.ID).Scan(&n))
	if n != 1 {
		t.Fatal("duplicate analysis", n)
	}
	es, err := s.Evidence(ctx, o.JobID)
	must(t, err)
	if len(es) != 7 {
		t.Fatal("duplicate evidence", len(es))
	}
	pending, err := q.R.XPending(ctx, q.Stream(), q.Group()).Result()
	must(t, err)
	if pending.Count != 0 {
		t.Fatal("not ACKed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := w.Process(ctx, task); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM analysis_results WHERE observation_id=?", o.ID).Scan(&n))
	if n != 1 {
		t.Fatal("concurrent idempotency")
	}
	retryTask := p.NewTask("ANALYZE", o.ID)
	must(t, q.Publish(ctx, retryTask))
	rm := readOne(t, ctx, q, "retry")
	retried, err := q.Fail(ctx, rm.ID, retryTask, errors.New("503 temporary"), 2)
	must(t, err)
	if !retried {
		t.Fatal("transient not retried")
	}
	count, err := q.R.ZCard(ctx, q.Prefix+"retry:ANALYZE").Result()
	must(t, err)
	if count != 1 {
		t.Fatal(count)
	}
	scheduled, err := q.Schedule(ctx)
	must(t, err)
	if scheduled != 0 {
		t.Fatal("backoff ignored")
	}
	zs, err := q.R.ZRange(ctx, q.Prefix+"retry:ANALYZE", 0, -1).Result()
	must(t, err)
	must(t, q.R.ZAdd(ctx, q.Prefix+"retry:ANALYZE", redis.Z{Score: 0, Member: zs[0]}).Err())
	var total atomic.Int64
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := q.Schedule(ctx)
			if err != nil {
				t.Error(err)
			}
			total.Add(int64(n))
		}()
	}
	wg.Wait()
	if total.Load() != 1 {
		t.Fatal("duplicate scheduling", total.Load())
	}
	rm = readOne(t, ctx, q, "retry")
	rt, err := pipeline.Decode(rm)
	must(t, err)
	if rt.Attempt != 2 {
		t.Fatal(rt)
	}
	retried, err = q.Fail(ctx, rm.ID, rt, errors.New("503 again"), 2)
	must(t, err)
	if retried {
		t.Fatal("max attempt exceeded")
	}
	dlq, err := q.DLQ(ctx)
	must(t, err)
	if len(dlq) != 1 {
		t.Fatal(dlq)
	}
	must(t, q.Redrive(ctx, rt.ID))
	dlq, err = q.DLQ(ctx)
	must(t, err)
	if len(dlq) != 0 {
		t.Fatal("redrive retained")
	}
	red := readOne(t, ctx, q, "redrive")
	must(t, q.Ack(ctx, red.ID))
	perm := p.NewTask("ANALYZE", o.ID)
	must(t, q.Publish(ctx, perm))
	pm := readOne(t, ctx, q, "permanent")
	retried, err = q.Fail(ctx, pm.ID, perm, errors.New("403 auth error"), 5)
	must(t, err)
	if retried {
		t.Fatal("permanent retried")
	}
	var accepted atomic.Int64
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := q.Allow(ctx, "source:fixture", 7, time.Minute)
			if err != nil {
				t.Error(err)
			}
			if ok {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 7 {
		t.Fatal("rate limiter not atomic", accepted.Load())
	}
	t.Log("G-K: Stream, ACK, commit-before-ACK recovery, database idempotency, atomic retry, DLQ/redrive and sliding window passed")
}
func TestMyRPCTwoInstances(t *testing.T) {
	ctx, _, _, _, _ := setup(t)
	endpoints := config.Load().Endpoints()
	name := "ct-integration-" + d.ID()
	s1, err := analysis.StartServer(ctx, endpoints, "127.0.0.1:0", "", "analysis-1", analysis.RuleExtractor{}, 2, 0, name)
	must(t, err)
	defer s1.Close()
	s2, err := analysis.StartServer(ctx, endpoints, "127.0.0.1:0", "", "analysis-2", analysis.RuleExtractor{}, 2, 0, name)
	must(t, err)
	defer s2.Close()
	c, err := analysis.NewClient(endpoints, 2, time.Second, name)
	must(t, err)
	defer c.Close()
	seen := map[string]int{}
	for i := 0; i < 8; i++ {
		v, err := c.Call(ctx, analysis.Request{Text: jd})
		must(t, err)
		seen[v.Instance]++
		if len(v.Claims) == 0 {
			t.Fatal("no business claims")
		}
	}
	if seen["analysis-1"] == 0 || seen["analysis-2"] == 0 {
		t.Fatal("not balanced", seen)
	}
	// Abrupt serving failure without lease revocation exercises stale discovery + breaker.
	s2.Server.Shutdown()
	success, failed := 0, 0
	for i := 0; i < 28; i++ {
		v, err := c.Call(ctx, analysis.Request{Text: jd})
		if err != nil {
			failed++
			if strings.Contains(err.Error(), "circuit breaker open") {
				t.Fatal("known open instance must be filtered while a healthy peer exists")
			}
		} else {
			success++
			if v.Instance != "analysis-1" {
				t.Fatal(v)
			}
		}
	}
	if success != 22 || failed != 6 {
		t.Fatal("expected six failures to complete the existing breaker window, then healthy selection", success, failed)
	}
	for i := 0; i < 12; i++ {
		v, e := c.Call(ctx, analysis.Request{Text: jd})
		must(t, e)
		if v.Instance != "analysis-1" {
			t.Fatal("breaker-open peer consumed a healthy request", v)
		}
	}
	must(t, s2.Registry.Close())
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		instances, err := s1.Registry.DiscoverContext(ctx, name)
		must(t, err)
		if len(instances) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	for i := 0; i < 4; i++ {
		v, err := c.Call(ctx, analysis.Request{Text: jd})
		must(t, err)
		if v.Instance != "analysis-1" {
			t.Fatal(v)
		}
	}
	slowName := name + "-slow"
	slow, err := analysis.StartServer(ctx, endpoints, "127.0.0.1:0", "", "slow", analysis.RuleExtractor{}, 1, 300*time.Millisecond, slowName)
	must(t, err)
	defer slow.Close()
	sc, err := analysis.NewClient(endpoints, 1, time.Second, slowName)
	must(t, err)
	defer sc.Close()
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	start := time.Now()
	_, err = sc.Call(short, analysis.Request{Text: jd})
	cancel()
	if err == nil || time.Since(start) > 500*time.Millisecond {
		t.Fatal("timeout not bounded", err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	_, err = sc.Call(cancelled, analysis.Request{Text: jd})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	t.Logf("L: two instances %v; after failure successful=%d errors=%d breaker-aware selection; timeout and client cancellation passed", seen, success, failed)
}
func TestAgentConfirmationSSEMCP(t *testing.T) {
	ctx, s, q, user, src := setup(t)
	o, err := s.Ingest(ctx, ingest(t, ctx, s, src))
	must(t, err)
	w := worker(s, q)
	must(t, w.Process(ctx, p.NewTask("ANALYZE", o.ID)))
	must(t, s.Assess(ctx, d.ID(), o.JobID))
	must(t, s.SaveProfile(ctx, user, d.Profile{GraduationYear: 2027, Degree: "BACHELOR", PreferredTypes: []string{"FULL_TIME"}, PreferredCities: []string{"Shanghai"}, Skills: []string{"go"}}))
	rg := &rag.Service{Store: s, Sem: make(chan struct{}, 1)}
	tools := &agent.Tools{Store: s, Queue: q, RAG: rg}
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	pending, err := tools.Propose(ctx, user, "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, err)
	apps, err := s.Owned(ctx, "applications", user)
	must(t, err)
	if len(apps) != 0 {
		t.Fatal("proposal executed write")
	}
	if _, err = tools.Confirm(ctx, other, pending.ID); err == nil {
		t.Fatal("owner bypass")
	}
	out, err := tools.Confirm(ctx, user, pending.ID)
	must(t, err)
	again, err := tools.Confirm(ctx, user, pending.ID)
	must(t, err)
	var firstApp, secondApp d.Application
	must(t, json.Unmarshal(out, &firstApp))
	must(t, json.Unmarshal(again, &secondApp))
	if firstApp.ID != secondApp.ID || firstApp.Version != secondApp.Version {
		t.Fatal("confirmation not idempotent")
	}
	var app d.Application
	must(t, json.Unmarshal(out, &app))
	transition, err := tools.Propose(ctx, user, "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, State: "APPLIED", Version: 1})))
	must(t, err)
	_, err = s.ApplyAction(ctx, user, d.ID(), "transition_application", transition.Args)
	must(t, err)
	if _, err = tools.Confirm(ctx, user, transition.ID); !errors.Is(err, p.ErrConflict) {
		t.Fatal("stale confirmation bypass", err)
	}
	expired, err := tools.Propose(ctx, user, "create_application", pending.Args)
	must(t, err)
	must(t, q.R.PExpire(ctx, q.Prefix+"pending:"+expired.ID, time.Millisecond).Err())
	time.Sleep(5 * time.Millisecond)
	if _, err = tools.Confirm(ctx, user, expired.ID); err == nil {
		t.Fatal("expired action")
	}
	runtime := &agent.Runtime{Model: agent.DemoModel{}, Tools: tools, R: q.R, Prefix: q.Prefix, MaxModels: 4, MaxTools: 8, Deadline: time.Second, ToolTimeout: time.Second}
	result := runtime.Run(ctx, user, "session", "为什么岗位 "+o.JobID+" OPEN?", nil)
	if result.Terminal != "COMPLETED" || result.Executed != 3 {
		t.Fatal(result)
	}
	trace, err := q.R.Get(ctx, q.Prefix+"agent:trace:"+user+":"+result.RunID).Result()
	must(t, err)
	if strings.Contains(trace, jd) {
		t.Fatal("private text in trace")
	}
	if n, _ := q.R.Exists(ctx, q.Prefix+"agent:session:"+other+":session").Result(); n != 0 {
		t.Fatal("session ownership")
	}
	secret := "test-secret-32-bytes-minimum-value"
	authn := auth.Service{Store: s, Secret: []byte(secret)}
	token, err := authn.Token(user)
	must(t, err)
	api := &transport.API{Store: s, Queue: q, Tools: tools, Agent: runtime, Auth: authn, Metrics: observability.New()}
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/agent/stream", strings.NewReader(`{"session_id":"sse","message":"我投过哪些岗位"}`))
	must(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	must(t, err)
	for _, event := range []string{"run_start", "model_start", "model_end", "tool_start", "tool_result", "final"} {
		if !strings.Contains(string(b), "event: "+event) {
			t.Fatal("missing SSE event", event, string(b))
		}
	}
	unauth, err := http.Post(server.URL+"/agent/decide", "application/json", strings.NewReader(`{}`))
	must(t, err)
	unauth.Body.Close()
	if unauth.StatusCode != 401 {
		t.Fatal("agent unauthenticated")
	}
	// Real subprocess stdio protocol: Connect performs initialize + initialized.
	root := filepath.Join("..", "..")
	binary := filepath.Join(t.TempDir(), "mcp-server")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/mcp-server")
	build.Dir = root
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("MCP build: %v %s", err, output)
	}
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "JWT_SECRET="+secret, "MCP_TOKEN="+token, "MYSQL_DSN="+config.Env("MYSQL_TEST_DSN", strings.Replace(config.Load().DSN, "/campustrace?", "/campustrace_test?", 1)))
	client := mcp.NewClient(&mcp.Implementation{Name: "integration-host", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	must(t, err)
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	must(t, err)
	if len(listed.Tools) != 5 {
		t.Fatal("MCP tools", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if agent.IsWrite(tool.Name) {
			t.Fatal("MCP write exposed")
		}
	}
	called, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_job", Arguments: map[string]any{"job_id": o.JobID}})
	must(t, err)
	if called.IsError || len(called.Content) == 0 {
		t.Fatal(called)
	}
	t.Log("N-R: grounded tool reads; pending confirmation/ownership/TTL/version; SSE lifecycle; real MCP stdio initialize/ListTools/CallTool passed")
}
func TestSSEDisconnect(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("integration")
	}
	cancelled := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transport.SSE(w, r, func(ctx context.Context, emit func(agent.Event) error) {
			emit(agent.Event{Type: "run_start", Data: "start"})
			<-ctx.Done()
			close(cancelled)
		})
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "POST", srv.URL, nil)
	must(t, err)
	resp, err := http.DefaultClient.Do(req)
	must(t, err)
	buf := make([]byte, 8)
	_, err = resp.Body.Read(buf)
	must(t, err)
	cancel()
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("client disconnect did not cancel runtime")
	}
}

type measuredAnalyzer struct {
	active, max atomic.Int64
	rpc         *analysis.RPCClient
}

func (m *measuredAnalyzer) Analyze(ctx context.Context, o d.Observation, corr string) ([]d.Claim, error) {
	n := m.active.Add(1)
	defer m.active.Add(-1)
	for {
		old := m.max.Load()
		if old >= n || m.max.CompareAndSwap(old, n) {
			break
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(15 * time.Millisecond):
	}
	return m.rpc.Analyze(ctx, o, corr)
}
func TestRunningWorkerOutboxRPC(t *testing.T) {
	ctx, s, q, _, src := setup(t)
	name := "running-" + d.ID()
	srv, err := analysis.StartServer(ctx, config.Load().Endpoints(), "127.0.0.1:0", "", "running-analysis", analysis.RuleExtractor{}, 2, 0, name)
	must(t, err)
	defer srv.Close()
	rpc, err := analysis.NewClient(config.Load().Endpoints(), 1, time.Second, name)
	must(t, err)
	defer rpc.Close()
	meter := &measuredAnalyzer{rpc: rpc}
	w := worker(s, q)
	w.Analyzer = meter
	in := ingest(t, ctx, s, src)
	ids := []string{}
	for i := 0; i < 8; i++ {
		in.ExternalID = d.ID()
		in.Text = jd + "\nfixture: " + d.ID()
		o, err := s.Ingest(ctx, in)
		must(t, err)
		ids = append(ids, o.ID)
	}
	runctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			must(t, err)
		case <-time.After(4 * time.Second):
			t.Error("worker did not stop")
		}
	}()
	until := time.Now().Add(10 * time.Second)
	for {
		complete := 0
		for _, id := range ids {
			ok, err := s.Analyzed(ctx, id, d.ProcessingVersion("", d.ParserVersion, d.ParserVersion))
			must(t, err)
			if ok {
				complete++
			}
		}
		if complete == len(ids) {
			break
		}
		if time.Now().After(until) {
			t.Fatal("worker failed to drain", complete)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if meter.max.Load() > 2 || meter.max.Load() == 0 {
		t.Fatal("unbounded or unused pool", meter.max.Load())
	}
	t.Logf("Actual bounded worker -> MyRPC -> SQL -> outbox pipeline completed %d observations; max processing calls=%d, RPC semaphore=1", len(ids), meter.max.Load())
}

func TestFailureStatusAndOwnership(t *testing.T) {
	ctx, s, q, user, src := setup(t)
	o, err := s.Ingest(ctx, ingest(t, ctx, s, src))
	must(t, err)
	must(t, s.AnalysisFailed(ctx, o.ID, "SCHEMA"))
	must(t, s.Assess(ctx, d.ID(), o.JobID))
	j, err := s.Job(ctx, o.JobID)
	must(t, err)
	if j.CurrentStatus != "UNKNOWN" {
		t.Fatal("analysis failure became authoritative", j)
	}
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	raw, err := s.ApplyAction(ctx, user, d.ID(), "create_application", []byte(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, err)
	var app d.Application
	must(t, json.Unmarshal(raw, &app))
	if _, err = s.ApplyAction(ctx, other, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: app.ID, State: "APPLIED", Version: 1}))); err == nil {
		t.Fatal("cross-user transition")
	}
	if _, err = s.SaveInterview(ctx, other, d.Interview{ApplicationID: app.ID, Round: 1, ScheduledAt: time.Now()}); err == nil {
		t.Fatal("cross-user interview")
	}
	project, err := s.SaveProject(ctx, user, d.Project{Name: "Owned project"})
	must(t, err)
	if _, err = s.SaveFact(ctx, other, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: "forged", Verified: true}); err == nil {
		t.Fatal("cross-user fact")
	}
	tools := &agent.Tools{Store: s, Queue: q, RAG: &rag.Service{Store: s, Sem: make(chan struct{}, 1)}}
	runtime := &agent.Runtime{Model: &agent.Scripted{Err: errors.New("private provider error")}, Tools: tools, R: q.R, Prefix: q.Prefix, MaxModels: 4, MaxTools: 8, Deadline: time.Second, ToolTimeout: time.Second}
	result := runtime.Run(ctx, user, "failed", "test", nil)
	if result.Terminal != "ERROR" {
		t.Fatal(result)
	}
	b, err := q.R.Get(ctx, q.Prefix+"agent:trace:"+user+":"+result.RunID).Result()
	must(t, err)
	if strings.Contains(b, "private provider error") {
		t.Fatal("unsanitized error in trace")
	}
}
