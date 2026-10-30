package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"github.com/KanaDoodle/CampusTrace/internal/modelcontrol"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

const controlTestSecret = "synthetic-model-control-api-secret-32"

// This adapter exists only in the test binary. Production endpoint validation
// remains unchanged; the provider is a parent-owned loopback HTTP fixture.
type controlHTTPModel struct{ URL string }

func (m controlHTTPModel) Complete(ctx context.Context, messages, tools any) (json.RawMessage, error) {
	req, e := http.NewRequestWithContext(ctx, "POST", m.URL, strings.NewReader(d.JSON(map[string]any{"messages": messages, "tools": tools})))
	if e != nil {
		return nil, e
	}
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &analysis.HTTPError{Status: resp.StatusCode}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func runModelControlAPI(t *testing.T, ctx context.Context, s *p.Store, q *pipeline.Queue, c adversarialConfig) {
	t.Helper()
	control, e := modelcontrol.New(ctx, s, modelcontrol.Options{Policy: c.ModelPolicy, Wait: 4 * time.Second, Execution: 2 * time.Second, Grace: 100 * time.Millisecond, Poll: 10 * time.Millisecond})
	must(t, e)
	a := &transport.API{Store: s, Queue: q, Auth: auth.Service{Store: s, Secret: []byte(controlTestSecret)}, Metrics: observability.New(), ResumeModel: controlHTTPModel{c.ProviderURL}, ResumeModelName: "control-fixture", ModelControl: control}
	runner := a.StartMatchTasks(ctx, 2)
	defer runner.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	must(t, e)
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: time.Second}
	defer server.Close()
	go server.Serve(listener)
	must(t, q.R.Set(ctx, q.Prefix+"api:"+c.Entity, "http://"+listener.Addr().String(), 0).Err())
	<-ctx.Done()
}

type controlReply struct {
	Status int
	Body   []byte
	Err    error
}

func controlRequest(ctx context.Context, address, token, path string, body any) controlReply {
	method := "POST"
	var input io.Reader
	if body == nil {
		method = "GET"
	} else {
		input = bytes.NewBufferString(d.JSON(body))
	}
	req, e := http.NewRequestWithContext(ctx, method, address+path, input)
	if e != nil {
		return controlReply{Err: e}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		return controlReply{Err: e}
	}
	defer resp.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return controlReply{resp.StatusCode, raw, e}
}
func requireControlReply(t *testing.T, r controlReply, status int) {
	t.Helper()
	if r.Err != nil || r.Status != status {
		t.Fatalf("HTTP got %d %s error=%v; want %d", r.Status, r.Body, r.Err, status)
	}
}
func controlEventually(t *testing.T, condition func() bool) {
	t.Helper()
	until := time.Now().Add(8 * time.Second)
	for time.Now().Before(until) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("model-control condition did not converge")
}
func controlFixture(t *testing.T, ctx context.Context, s *p.Store, user string, count int) (string, map[string]any) {
	t.Helper()
	must(t, s.SaveProfile(ctx, user, d.Profile{Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}}))
	ids := []string{}
	for i := 0; i < count; i++ {
		o, e := s.IngestForUser(ctx, user, p.Ingest{Company: "Control Synthetic", Title: fmt.Sprintf("服务端开发%d", i), JobType: "FULL_TIME", ExternalID: d.ID(), Text: fmt.Sprintf("使用 Go 开发后端服务，负责模块 %d。", i), FetchStatus: "SUCCESS"})
		must(t, e)
		ids = append(ids, o.JobID)
	}
	snap, e := s.MatchSnapshot(ctx, user, matching.ModelIdentity("server-default", "control-fixture"), "", ids)
	must(t, e)
	token, e := (auth.Service{Store: s, Secret: []byte(controlTestSecret)}).Token(user)
	must(t, e)
	return token, map[string]any{"job_ids": ids, "candidate_hash": snap.CandidateHash, "input_keys": taskInputKeys(snap, ids), "request_key": d.ID()}
}
func startControlAPI(t *testing.T, ctx context.Context, q *pipeline.Queue, policy p.ModelPoolPolicy, provider, instance string) (*adversarialChild, string) {
	t.Helper()
	child := startAdversarial(t, adversarialConfig{Role: "model-api", Prefix: q.Prefix, Entity: instance, ModelPolicy: policy, ProviderURL: provider})
	adversarialWait(t, ctx, q.R, q.Prefix+"api:"+instance)
	return child, q.R.Get(ctx, q.Prefix+"api:"+instance).Val()
}

func TestModelControlTwoAPIProcessesShareCapacityFairnessAndReplay(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, q, a, _ := setup(t)
	_, _, _, b, _ := setup(t)
	policy := modelPolicy()
	policy.DailyLimit = 20
	policy.LeaseMS = 1500
	must(t, s.EnsureModelPool(ctx, policy))
	tokenA, bodyA := controlFixture(t, ctx, s, a, 7)
	tokenB, bodyB := controlFixture(t, ctx, s, b, 1)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var active, peak, calls atomic.Int32
	var mu sync.Mutex
	order := []int{}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Messages json.RawMessage `json:"messages"`
		}
		if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
			http.Error(w, "fixture", 400)
			return
		}
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		var msgs []map[string]string
		_ = json.Unmarshal(in.Messages, &msgs)
		var input matching.HolisticRequest
		_ = json.Unmarshal([]byte(msgs[1]["content"]), &input)
		mu.Lock()
		order = append(order, len(input.Jobs))
		mu.Unlock()
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-time.After(5 * time.Second):
			}
		}
		raw, e := (&matchFixtureModel{}).Complete(r.Context(), in.Messages, nil)
		if e != nil {
			http.Error(w, "fixture", 500)
			return
		}
		w.Write(raw)
	}))
	defer provider.Close()
	_, apiA := startControlAPI(t, ctx, q, policy, provider.URL, "a")
	_, apiB := startControlAPI(t, ctx, q, policy, provider.URL, "b")
	first := controlRequest(ctx, apiA, tokenA, "/api/matching/tasks", bodyA)
	requireControlReply(t, first, 202)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not receive first batch")
	}
	replay := controlRequest(ctx, apiB, tokenA, "/api/matching/tasks", bodyA)
	requireControlReply(t, replay, 202)
	var run, replayed p.MatchRun
	must(t, json.Unmarshal(first.Body, &run))
	must(t, json.Unmarshal(replay.Body, &replayed))
	if run.ID != replayed.ID {
		t.Fatal("cross-process replay created another run")
	}
	second := controlRequest(ctx, apiB, tokenB, "/api/matching/tasks", bodyB)
	requireControlReply(t, second, 202)
	var small p.MatchRun
	must(t, json.Unmarshal(second.Body, &small))
	controlEventually(t, func() bool {
		stats, e := s.ModelPoolStatus(ctx, policy.ID)
		return e == nil && stats.Waiting == 1 && stats.Active == 1
	})
	once.Do(func() { close(release) })
	controlEventually(t, func() bool {
		big, e := s.MatchRun(ctx, a, run.ID)
		little, e2 := s.MatchRun(ctx, b, small.ID)
		return e == nil && e2 == nil && big.State == "COMPLETED" && little.State == "COMPLETED"
	})
	mu.Lock()
	got := fmt.Sprint(order)
	mu.Unlock()
	if peak.Load() != 1 || calls.Load() != 4 || got != "[3 1 3 1]" {
		t.Fatal("capacity/fairness/replay", peak.Load(), calls.Load(), got)
	}
	stats, e := s.ModelPoolStatus(ctx, policy.ID)
	must(t, e)
	if stats.Active != 0 || stats.Waiting != 0 || stats.AllocatedToday != 4 {
		t.Fatal(stats)
	}
	adversarialEvent(map[string]any{"event": "model_two_api_verified", "provider_peak": peak.Load(), "calls": calls.Load(), "batch_order": got})
}

func TestModelControlAPICrashBeforeAndDuringProviderCall(t *testing.T) {
	adversarialEnabled(t)
	for _, phase := range []string{"QUEUED", "CALLING"} {
		t.Run(phase, func(t *testing.T) {
			ctx, s, q, u, _ := setup(t)
			_, _, _, blocker, _ := setup(t)
			policy := modelPolicy()
			policy.DailyLimit = 10
			policy.LeaseMS = 500
			must(t, s.EnsureModelPool(ctx, policy))
			token, body := controlFixture(t, ctx, s, u, 1)
			body = map[string]any{"job_ids": body["job_ids"], "candidate_hash": body["candidate_hash"]}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var count atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				close(entered)
				select {
				case <-release:
				case <-time.After(5 * time.Second):
				}
				w.Write([]byte(`{"content":"{}"}`))
			}))
			defer provider.Close()
			if phase == "QUEUED" {
				grant := grantModel(t, ctx, s, policy, blocker)
				_, e := s.BeginModelCall(ctx, grant, d.Hash("blocker"), d.Hash("fixture"), "", 2*time.Second, 0)
				must(t, e)
			}
			child, address := startControlAPI(t, ctx, q, policy, provider.URL, "crash")
			replied := make(chan controlReply, 1)
			go func() { replied <- controlRequest(ctx, address, token, "/api/matching/analyze", body) }()
			controlEventually(t, func() bool {
				var n int
				e := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM model_resource_tickets WHERE user_id=? AND state=?", u, phase).Scan(&n)
				return e == nil && n == 1
			})
			if phase == "CALLING" {
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("provider not called")
				}
			}
			child.stop(true)
			select {
			case <-replied:
			case <-time.After(3 * time.Second):
				t.Fatal("killed API connection did not close")
			}
			controlEventually(t, func() bool {
				calls, e := s.ModelCalls(ctx, u, policy.ID)
				if e != nil {
					return false
				}
				if phase == "QUEUED" {
					var n int
					_ = s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM model_resource_tickets WHERE user_id=? AND state='EXPIRED'", u).Scan(&n)
					return n == 1 && len(calls) == 0
				}
				return len(calls) == 1 && calls[0].State == "UNKNOWN"
			})
			if phase == "QUEUED" {
				if count.Load() != 0 {
					t.Fatal("queued kill called provider")
				}
				return
			}
			_, other := startControlAPI(t, ctx, q, policy, provider.URL, "recovered")
			blocked := controlRequest(ctx, other, token, "/api/matching/analyze", body)
			requireControlReply(t, blocked, 409)
			if !bytes.Contains(blocked.Body, []byte("MODEL_REVIEW_REQUIRED")) || count.Load() != 1 {
				t.Fatal("unknown call replayed", blocked, count.Load())
			}
			calls, e := s.ModelCalls(ctx, u, policy.ID)
			must(t, e)
			review := p.ModelCallReview{Version: calls[0].Version, Decision: "USED", Verified: true, Note: "已核对合成提供商记录，请求已经收到并执行。"}
			path := "/api/matching/calls/" + calls[0].ID + "/review"
			first := controlRequest(ctx, other, token, path, review)
			requireControlReply(t, first, 200)
			second := controlRequest(ctx, other, token, path, review)
			requireControlReply(t, second, 200)
			if count.Load() != 1 || !bytes.Equal(first.Body, second.Body) {
				t.Fatal("review replay called provider or changed receipt")
			}
			once.Do(func() { close(release) })
		})
	}
}

type controlModelFunc func(context.Context, any, any) (json.RawMessage, error)

func (f controlModelFunc) Complete(ctx context.Context, m, t any) (json.RawMessage, error) {
	return f(ctx, m, t)
}

func TestModelControlControllerHeartbeatRejectionCancellationAndPreflight(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	policy := modelPolicy()
	policy.DailyLimit = 10
	policy.LeaseMS = 150
	control, e := modelcontrol.New(ctx, s, modelcontrol.Options{Policy: policy, Wait: time.Second, Execution: 2 * time.Second, Grace: time.Millisecond * 50, Poll: time.Millisecond * 10})
	must(t, e)
	var calls atomic.Int32
	model := controlModelFunc(func(ctx context.Context, _, _ any) (json.RawMessage, error) {
		calls.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(400 * time.Millisecond):
		}
		return json.RawMessage(`{}`), nil
	})
	_, e = control.Complete(ctx, u, "fixture", model, []string{"synthetic"}, nil, nil)
	must(t, e)
	rows, e := s.ModelCalls(ctx, u, policy.ID)
	must(t, e)
	if len(rows) != 1 || rows[0].State != "RESPONSE_RECEIVED" {
		t.Fatal("heartbeat failed", rows)
	}
	_, e = control.Complete(ctx, u, "fixture", model, nil, nil, nil, func(context.Context) error { return p.ErrStaleInput })
	if !errors.Is(e, p.ErrStaleInput) || calls.Load() != 1 {
		t.Fatal("stale preflight reached provider", e, calls.Load())
	}
	_, e = control.Complete(ctx, u, "fixture", model, nil, nil, func() {}, func(context.Context) error { return context.Canceled })
	if !errors.Is(e, context.Canceled) || calls.Load() != 1 {
		t.Fatal(e, calls.Load())
	}
	rejected := controlModelFunc(func(context.Context, any, any) (json.RawMessage, error) { return nil, &analysis.HTTPError{Status: 429} })
	_, e = control.Complete(ctx, u, "fixture", rejected, nil, nil, nil)
	var status *analysis.HTTPError
	if !errors.As(e, &status) || status.Status != 429 {
		t.Fatal(e)
	}
	if e = s.CheckModelCallReview(ctx, u, policy.ID, ""); e != nil {
		t.Fatal("explicit rejection quarantined", e)
	}
	callCtx, cancel := context.WithCancel(ctx)
	_, e = control.Complete(callCtx, u, "fixture", model, nil, nil, cancel)
	if !errors.Is(e, context.Canceled) || calls.Load() != 1 {
		t.Fatal("cancel before send called provider", e, calls.Load())
	}
	stats, e := s.ModelPoolStatus(ctx, policy.ID)
	must(t, e)
	if stats.AllocatedToday != 2 || stats.Active != 0 || stats.Waiting != 0 {
		t.Fatal("preflight/cancellation consumed capacity or quota", stats)
	}
}

func TestModelControlDailyBudgetRaceAcrossAPIProcesses(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, q, a, _ := setup(t)
	_, _, _, b, _ := setup(t)
	policy := modelPolicy()
	policy.Capacity = 2
	policy.DailyLimit = 1
	policy.LeaseMS = 1500
	must(t, s.EnsureModelPool(ctx, policy))
	ta, ba := controlFixture(t, ctx, s, a, 1)
	tb, bb := controlFixture(t, ctx, s, b, 1)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in struct {
			Messages json.RawMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "fixture", 400)
			return
		}
		raw, e := (&matchFixtureModel{}).Complete(r.Context(), in.Messages, nil)
		if e != nil {
			http.Error(w, "fixture", 500)
			return
		}
		w.Write(raw)
	}))
	defer provider.Close()
	_, aa := startControlAPI(t, ctx, q, policy, provider.URL, "budget-a")
	_, ab := startControlAPI(t, ctx, q, policy, provider.URL, "budget-b")
	responses := make(chan controlReply, 2)
	for _, in := range []struct {
		address, token string
		body           map[string]any
	}{{aa, ta, ba}, {ab, tb, bb}} {
		go func() {
			responses <- controlRequest(ctx, in.address, in.token, "/api/matching/analyze", map[string]any{"job_ids": in.body["job_ids"], "candidate_hash": in.body["candidate_hash"]})
		}()
	}
	success, limited := 0, 0
	for i := 0; i < 2; i++ {
		r := <-responses
		if r.Err != nil {
			t.Fatal(r.Err)
		}
		switch r.Status {
		case 200:
			success++
		case 429:
			if !bytes.Contains(r.Body, []byte("MODEL_GLOBAL_BUDGET")) {
				t.Fatal(string(r.Body))
			}
			limited++
		default:
			t.Fatal(r.Status, string(r.Body))
		}
	}
	stats, e := s.ModelPoolStatus(ctx, policy.ID)
	must(t, e)
	var userTotal int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COALESCE(SUM(calls),0) FROM match_daily_usage WHERE user_id IN (?,?)", a, b).Scan(&userTotal))
	if success != 1 || limited != 1 || calls.Load() != 1 || stats.AllocatedToday != 1 || stats.Active != 0 || userTotal != 1 {
		t.Fatal(success, limited, calls.Load(), stats, userTotal)
	}
}

func TestModelControlDatabaseWaitFailsClosedAndRecovers(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	policy := modelPolicy()
	policy.LeaseMS = 1500
	control, e := modelcontrol.New(ctx, s, modelcontrol.Options{Policy: policy, Wait: time.Second, Execution: time.Second})
	must(t, e)
	var calls atomic.Int32
	model := controlModelFunc(func(context.Context, any, any) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{}`), nil
	})
	s.DB.SetMaxOpenConns(1)
	connection, e := s.DB.Conn(ctx)
	must(t, e)
	blocked, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	_, e = control.Complete(blocked, u, "fixture", model, nil, nil, nil)
	cancel()
	must(t, connection.Close())
	if !errors.Is(e, p.ErrBackendUnavailable) || calls.Load() != 0 {
		t.Fatal("SQL admission failure called provider", e, calls.Load())
	}
	_, e = control.Complete(ctx, u, "fixture", model, nil, nil, nil)
	must(t, e)
	if calls.Load() != 1 {
		t.Fatal("did not recover", calls.Load())
	}
}

func TestModelControlLostHeartbeatQuarantinesAndFencesResponse(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	policy := modelPolicy()
	policy.LeaseMS = 150
	control, e := modelcontrol.New(ctx, s, modelcontrol.Options{Policy: policy, Wait: time.Second, Execution: 2 * time.Second, Grace: time.Millisecond * 100})
	must(t, e)
	entered := make(chan struct{})
	finished := make(chan error, 1)
	model := controlModelFunc(func(ctx context.Context, _, _ any) (json.RawMessage, error) {
		close(entered)
		<-ctx.Done()
		return json.RawMessage(`{"late":"must not publish"}`), nil
	})
	go func() {
		raw, e := control.Complete(ctx, u, "fixture", model, nil, nil, nil)
		if raw != nil && e == nil {
			e = errors.New("late result survived lost ownership")
		}
		finished <- e
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("call did not start")
	}
	_, e = s.DB.ExecContext(ctx, "UPDATE model_resource_tickets SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE user_id=? AND state='CALLING'", u)
	must(t, e)
	select {
	case e := <-finished:
		if !errors.Is(e, p.ErrModelLease) {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("lost heartbeat did not cancel local caller")
	}
	rows, e := s.ModelCalls(ctx, u, policy.ID)
	must(t, e)
	stats, e := s.ModelPoolStatus(ctx, policy.ID)
	must(t, e)
	if len(rows) != 1 || rows[0].State != "UNKNOWN" || stats.Quarantined != 1 || stats.AllocatedToday != 1 {
		t.Fatal(rows, stats)
	}
}
