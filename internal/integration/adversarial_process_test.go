package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/redis/go-redis/v9"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type adversarialConfig struct {
	Role, Prefix, Owner, Entity, Mode, Gate string
	Capacity, OwnerCapacity, Count          int
}

func adversarialEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("CAMPUS_ADVERSARIAL") != "1" {
		t.Skip("requires disposable adversarial-local.sh")
	}
}
func adversarialEvent(v any) {
	if name := os.Getenv("CAMPUS_ADVERSARIAL_EVIDENCE"); name != "" {
		f, e := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e == nil {
			defer f.Close()
			_, _ = f.Write(append([]byte(d.JSON(v)), '\n'))
		}
	}
}

type adversarialChild struct {
	cmd  *exec.Cmd
	done chan error
	once sync.Once
	t    *testing.T
	role string
}

func startAdversarial(t *testing.T, c adversarialConfig) *adversarialChild {
	t.Helper()
	adversarialEnabled(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestAdversarialChild$", "-test.v")
	// Explicit synthetic settings only; never inherit provider or account secrets.
	for _, key := range []string{"PATH", "TMPDIR", "MYSQL_TEST_DSN", "REDIS_ADDR", "ETCD_ENDPOINTS", "CAMPUS_ADVERSARIAL_EVIDENCE"} {
		cmd.Env = append(cmd.Env, key+"="+os.Getenv(key))
	}
	cmd.Env = append(cmd.Env, "CAMPUS_INTEGRATION=1", "CAMPUS_ADVERSARIAL=1", "CAMPUS_ADVERSARIAL_CHILD="+d.JSON(c))
	logName := filepath.Join(filepath.Dir(os.Getenv("CAMPUS_ADVERSARIAL_EVIDENCE")), "child-"+d.ID()+".log")
	f, e := os.Create(logName)
	must(t, e)
	cmd.Stdout = f
	cmd.Stderr = f
	must(t, cmd.Start())
	v := &adversarialChild{cmd: cmd, done: make(chan error, 1), t: t, role: c.Role}
	adversarialEvent(map[string]any{"event": "start", "pid": cmd.Process.Pid, "role": c.Role, "mode": c.Mode, "prefix": c.Prefix, "log": logName})
	go func() { e := cmd.Wait(); f.Close(); v.done <- e }()
	t.Cleanup(func() { v.stop(true) })
	return v
}
func (c *adversarialChild) stop(kill bool) {
	c.once.Do(func() {
		if kill {
			_ = c.cmd.Process.Kill()
		}
		select {
		case e := <-c.done:
			rss := int64(0)
			if usage, ok := c.cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
				rss = usage.Maxrss
				if runtime.GOOS == "linux" {
					rss *= 1024
				}
			}
			adversarialEvent(map[string]any{"event": "reaped", "pid": c.cmd.Process.Pid, "kill_requested": kill, "exit_error": fmt.Sprint(e), "max_rss_bytes": rss})
			if !kill && e != nil {
				c.t.Error("child failed", c.role, c.cmd.Process.Pid, e)
			}
		case <-time.After(10 * time.Second):
			c.t.Error("owned child did not exit", c.cmd.Process.Pid)
		}
	})
}
func adversarialWait(t *testing.T, ctx context.Context, r *redis.Client, key string) {
	t.Helper()
	end := time.Now().Add(12 * time.Second)
	for time.Now().Before(end) {
		if r.Exists(ctx, key).Val() == 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("barrier not reached", key)
}
func childWait(ctx context.Context, r *redis.Client, key string) {
	for ctx.Err() == nil && r.Exists(ctx, key).Val() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
}

type adversarialAnalyzer struct {
	r *redis.Client
	c adversarialConfig
}

func (a adversarialAnalyzer) Analyze(ctx context.Context, o d.Observation, _ string) ([]d.Claim, error) {
	if a.c.Mode == "BEFORE_EXTERNAL" {
		a.r.Set(ctx, a.c.Prefix+"stage", a.c.Mode, 0)
		childWait(ctx, a.r, a.c.Prefix+"release")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	a.r.Incr(ctx, a.c.Prefix+"calls")
	if a.c.Mode == "VARIABLE" {
		a.r.HSetNX(ctx, a.c.Prefix+"first_start_ms", o.ID, time.Now().UnixMilli())
		n := a.r.HIncrBy(ctx, a.c.Prefix+"calls_by_observation", o.ID, 1).Val()
		behavior := a.r.Get(ctx, a.c.Prefix+"mock:"+o.ID).Val()
		if behavior == "429" && n == 1 {
			return nil, &analysis.HTTPError{Status: 429}
		}
		if behavior == "timeout" {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		delay := 10 * time.Millisecond
		if behavior == "slow" {
			delay = 120 * time.Millisecond
		}
		if behavior == "429" {
			delay = 60 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	a.r.Set(ctx, a.c.Prefix+"stage", "external_started", 0)
	if a.c.Mode == "DURING" || strings.HasPrefix(a.c.Mode, "BEFORE_SQL") {
		childWait(ctx, a.r, a.c.Prefix+"release")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if a.c.Mode == "BEFORE_SQL_NULL" {
		return nil, nil
	}
	return []d.Claim{}, nil
}

type adversarialHook struct {
	c adversarialConfig
	r *redis.Client
	s *p.Store
}

func (h adversarialHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, n, a string) (net.Conn, error) { return next(ctx, n, a) }
}
func (h adversarialHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h adversarialHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		args := cmd.Args()
		barrier := false
		if h.c.Mode == "BEFORE_CACHE" && cmd.Name() == "set" && len(args) > 1 && strings.Contains(fmt.Sprint(args[1]), "analysis:cache-v2:") {
			barrier = true
		}
		if h.c.Mode == "AFTER_SQL" && (cmd.Name() == "evalsha" || cmd.Name() == "eval") && len(args) > 3 && fmt.Sprint(args[3]) == h.c.Prefix+"tasks:ANALYZE" {
			o, e := h.s.Observation(ctx, h.c.Entity)
			barrier = e == nil && o.ExtractionStatus == "COMPLETE"
		}
		if barrier {
			h.r.Set(ctx, h.c.Prefix+"blocked", h.c.Mode, 0)
			childWait(ctx, h.r, h.c.Prefix+"continue")
		}
		return next(ctx, cmd)
	}
}

func TestAdversarialChild(t *testing.T) {
	raw := os.Getenv("CAMPUS_ADVERSARIAL_CHILD")
	if raw == "" {
		t.Skip("subprocess entry point")
	}
	adversarialEnabled(t)
	var c adversarialConfig
	must(t, json.Unmarshal([]byte(raw), &c))
	if !strings.HasPrefix(c.Prefix, "test:") {
		t.Fatal("synthetic namespace required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, e := p.Open(ctx, os.Getenv("MYSQL_TEST_DSN"))
	must(t, e)
	defer s.DB.Close()
	r := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDR"), ContextTimeoutEnabled: true})
	defer r.Close()
	q := &pipeline.Queue{R: r, Prefix: c.Prefix, Partitioned: true, Capacity: c.Capacity, OwnerCapacity: c.OwnerCapacity, ResolveOwner: s.TaskOwner}
	if c.Gate != "" {
		r.Incr(ctx, c.Prefix+"ready")
		childWait(ctx, r, c.Gate)
	}
	switch c.Role {
	case "publish":
		for i := 0; i < c.Count; i++ {
			task := p.NewTask("ASSESS", c.Entity)
			task.OwnerID = c.Owner
			e := q.Publish(ctx, task)
			if e == nil {
				r.Incr(ctx, c.Prefix+"accepted")
			} else if !errors.Is(e, pipeline.ErrQueueFull) {
				t.Fatal(e)
			}
		}
	case "outbox":
		e = s.Tx(ctx, func(tx *sql.Tx) error { return p.Outbox(ctx, tx, p.NewTask("ASSESS", c.Entity)) })
		if e == nil {
			r.Incr(ctx, c.Prefix+"accepted")
		} else if errors.Is(e, p.ErrQueueBusy) {
			r.Incr(ctx, c.Prefix+"rejected")
		} else {
			t.Fatal(e)
		}
	case "lease":
		run, replay, e := s.CreateMatchRun(ctx, c.Owner, "adversarial-request-fixed", d.ID(), p.MatchRun{CandidateHash: d.Hash("candidate"), Model: "synthetic", RequestHash: d.Hash("request"), Items: []p.MatchRunItem{{JobID: c.Entity, InputKey: d.Hash("input"), State: "QUEUED"}}})
		must(t, e)
		r.SAdd(ctx, c.Prefix+"run_ids", run.ID)
		if !replay {
			r.Incr(ctx, c.Prefix+"created")
		}
	case "lease-rival":
		_, _, e := s.CreateMatchRun(ctx, c.Owner, d.ID(), d.ID(), p.MatchRun{CandidateHash: d.Hash("candidate"), Model: "synthetic", RequestHash: d.Hash("rival"), Items: []p.MatchRunItem{{JobID: c.Entity, InputKey: d.Hash("input"), State: "QUEUED"}}})
		if !errors.Is(e, p.ErrMatchRunBusy) {
			t.Fatal("rival acquired active account", e)
		}
		r.Incr(ctx, c.Prefix+"rivals_rejected")
	case "worker":
		control := redis.NewClient(&redis.Options{Addr: os.Getenv("REDIS_ADDR"), ContextTimeoutEnabled: true})
		defer control.Close()
		if c.Mode == "BEFORE_CACHE" || c.Mode == "AFTER_SQL" {
			r.AddHook(adversarialHook{c, control, s})
		}
		w := &pipeline.Worker{Store: s, Queue: q, Analyzer: adversarialAnalyzer{control, c}, Concurrency: 1, Timeout: 2 * time.Second, ClaimIdle: 2500 * time.Millisecond, MaxAttempts: 3, Metrics: observability.New()}
		go func() { childWait(ctx, control, c.Prefix+"stop"); cancel() }()
		if e := w.Run(ctx); e != nil && ctx.Err() == nil {
			t.Fatal(e)
		}
	default:
		t.Fatal("unknown role")
	}
}

func TestAdversarialMultiprocessResidentCapacityAndMatchLease(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, q, u, _ := setup(t)
	q.Partitioned = true
	q.Capacity = 8
	q.OwnerCapacity = 3
	must(t, q.Init(ctx))
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Multiprocess", Title: "Go", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go 事务", FetchStatus: "SUCCESS"})
	must(t, e)
	children := []*adversarialChild{}
	gate := q.Prefix + "go"
	for i := 0; i < 6; i++ {
		children = append(children, startAdversarial(t, adversarialConfig{Role: "publish", Prefix: q.Prefix, Owner: d.ID(), Entity: o.JobID, Capacity: 8, OwnerCapacity: 3, Count: 20, Gate: gate}))
	}
	end := time.Now().Add(8 * time.Second)
	for q.R.Get(ctx, q.Prefix+"ready").Val() != "6" && time.Now().Before(end) {
		time.Sleep(5 * time.Millisecond)
	}
	must(t, q.R.Set(ctx, gate, 1, 0).Err())
	for _, c := range children {
		c.stop(false)
	}
	stats, e := q.Stats(ctx)
	must(t, e)
	var total int64
	for _, v := range stats {
		total += v.Outstanding
		if v.Outstanding > 8 {
			t.Fatal(v)
		}
	}
	if total != 8 || q.R.Get(ctx, q.Prefix+"accepted").Val() != "8" {
		t.Fatal("global capacity violation", total)
	}
	q.R.Del(ctx, gate, q.Prefix+"ready")
	children = nil
	for i := 0; i < 4; i++ {
		children = append(children, startAdversarial(t, adversarialConfig{Role: "lease", Prefix: q.Prefix, Owner: u, Entity: o.JobID, Gate: gate}))
	}
	time.Sleep(100 * time.Millisecond)
	q.R.Set(ctx, gate, 1, 0)
	for _, c := range children {
		c.stop(false)
	}
	if q.R.SCard(ctx, q.Prefix+"run_ids").Val() != 1 || q.R.Get(ctx, q.Prefix+"created").Val() != "1" {
		t.Fatal("cross-process idempotent lease failed")
	}
	for i := 0; i < 2; i++ {
		startAdversarial(t, adversarialConfig{Role: "lease-rival", Prefix: q.Prefix, Owner: u, Entity: o.JobID}).stop(false)
	}
	if q.R.Get(ctx, q.Prefix+"rivals_rejected").Val() != "2" {
		t.Fatal("account lease bypass")
	}
	ids := q.R.SMembers(ctx, q.Prefix+"run_ids").Val()
	var oldToken string
	must(t, s.DB.QueryRowContext(ctx, "SELECT token FROM match_runs WHERE id=?", ids[0]).Scan(&oldToken))
	_, e = s.DB.ExecContext(ctx, "UPDATE match_runs SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE id=?", ids[0])
	must(t, e)
	if e = s.UpdateMatchRun(ctx, u, p.MatchRunGuard{ID: ids[0], Token: oldToken}, func(v *p.MatchRun) error { return nil }); !errors.Is(e, p.ErrMatchRunLease) {
		t.Fatal("expired process token could write", e)
	}
	_, replay, e := s.CreateMatchRun(ctx, u, d.ID(), d.ID(), p.MatchRun{CandidateHash: d.Hash("candidate"), Model: "synthetic", RequestHash: d.Hash("replacement"), Items: []p.MatchRunItem{{JobID: o.JobID, InputKey: d.Hash("input"), State: "QUEUED"}}})
	must(t, e)
	if replay {
		t.Fatal("replacement replayed expired execution")
	}
}

func TestAdversarialMultiprocessLegacyMigrationAndDuplicates(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, q, u, _ := setup(t)
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Migration", Title: "Go", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go", FetchStatus: "SUCCESS"})
	must(t, e)
	_, e = s.DB.ExecContext(ctx, "UPDATE outbox SET sent=TRUE WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (?,?)", o.ID, o.JobID)
	must(t, e)
	legacy := &pipeline.Queue{R: q.R, Prefix: q.Prefix}
	must(t, legacy.Init(ctx))
	tasks := []p.Task{}
	for i := 0; i < 8; i++ {
		tasks = append(tasks, p.NewTask("ASSESS", o.JobID))
	}
	for _, task := range append(append([]p.Task{}, tasks[:5]...), tasks[0], tasks[1]) {
		must(t, legacy.Publish(ctx, task))
	}
	_, e = q.R.XReadGroup(ctx, &redis.XReadGroupArgs{Group: legacy.Group(), Consumer: "synthetic-dead-prior", Streams: []string{legacy.Stream(), ">"}, Count: 2}).Result()
	must(t, e)
	for _, v := range tasks[5:7] {
		v.Attempt = 2
		must(t, q.R.ZAdd(ctx, q.Prefix+"retry:ASSESS", redis.Z{Score: 0, Member: d.JSON(v)}).Err())
	}
	must(t, q.R.HSet(ctx, q.Prefix+"dlq", tasks[7].ID, d.JSON(pipeline.Failure{Task: tasks[7], Category: "PERMANENT"})).Err())
	q.Partitioned = true
	q.Capacity = 3
	q.OwnerCapacity = 2
	q.ResolveOwner = s.TaskOwner
	must(t, q.Init(ctx))
	must(t, q.Redrive(ctx, tasks[7].ID))
	a := startAdversarial(t, adversarialConfig{Role: "worker", Prefix: q.Prefix, Mode: "NORMAL", Capacity: 3, OwnerCapacity: 2})
	b := startAdversarial(t, adversarialConfig{Role: "worker", Prefix: q.Prefix, Mode: "NORMAL", Capacity: 3, OwnerCapacity: 2})
	end := time.Now().Add(15 * time.Second)
	count := 0
	for time.Now().Before(end) {
		count = 0
		for _, v := range tasks {
			var n int
			must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM completed_tasks WHERE id=?", v.ID).Scan(&n))
			count += n
		}
		groups, e := q.R.XInfoGroups(ctx, legacy.Stream()).Result()
		must(t, e)
		stats, e := q.Stats(ctx)
		must(t, e)
		resident := int64(0)
		for _, v := range stats {
			resident += v.Outstanding
			if v.Outstanding > 3 {
				t.Fatal("cross-process resident overflow", v)
			}
		}
		if count == 8 && q.R.XPending(ctx, legacy.Stream(), legacy.Group()).Val().Count == 0 && groups[0].Lag == 0 && q.R.ZCard(ctx, q.Prefix+"retry:ASSESS").Val() == 0 && resident == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if count != 8 {
		t.Fatal("migration lost work", count)
	}
	if q.R.XPending(ctx, legacy.Stream(), legacy.Group()).Val().Count != 0 || q.R.ZCard(ctx, q.Prefix+"retry:ASSESS").Val() != 0 || q.R.HLen(ctx, q.Prefix+"dlq").Val() != 0 {
		t.Fatal("legacy work stranded")
	}
	q.R.Set(ctx, q.Prefix+"stop", 1, 0)
	a.stop(false)
	b.stop(false)
}

func TestAdversarialMultiprocessVariableProviderLoad(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, q, u, _ := setup(t)
	_, _, _, small, _ := setup(t)
	observations := []d.Observation{}
	behaviors := []string{"slow", "fast", "429", "timeout", "fast", "slow"}
	for i, behavior := range behaviors {
		owner := u
		if i >= 4 {
			owner = small
		}
		o, e := s.IngestForUser(ctx, owner, p.Ingest{Company: "Synthetic Load", Title: fmt.Sprint("Go ", i), JobType: "FULL_TIME", ExternalID: d.ID(), Text: fmt.Sprint("Go 独立合成负载 ", i), FetchStatus: "SUCCESS"})
		must(t, e)
		observations = append(observations, o)
		must(t, q.R.Set(ctx, q.Prefix+"mock:"+o.ID, behavior, 0).Err())
	}
	started := time.Now()
	a := startAdversarial(t, adversarialConfig{Role: "worker", Prefix: q.Prefix, Mode: "VARIABLE"})
	b := startAdversarial(t, adversarialConfig{Role: "worker", Prefix: q.Prefix, Mode: "VARIABLE"})
	done := map[string]int64{}
	peak := int64(0)
	end := time.Now().Add(18 * time.Second)
	for time.Now().Before(end) {
		for _, o := range observations {
			v, e := s.Observation(ctx, o.ID)
			must(t, e)
			if v.ExtractionStatus == "COMPLETE" || v.ExtractionStatus == "FAILED" {
				if done[o.ID] == 0 {
					done[o.ID] = time.Now().UnixMilli()
				}
			}
		}
		stats, e := q.Stats(ctx)
		must(t, e)
		resident := int64(0)
		for _, v := range stats {
			resident += v.Outstanding
		}
		if resident > peak {
			peak = resident
		}
		if len(done) == 6 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(done) != 6 {
		t.Fatal("load did not settle", done)
	}
	rows := []map[string]any{}
	for i, o := range observations {
		calls := q.R.HGet(ctx, q.Prefix+"calls_by_observation", o.ID).Val()
		expected := "1"
		if behaviors[i] == "429" {
			expected = "2"
		}
		if calls != expected {
			t.Fatal("unexpected model replay", behaviors[i], calls)
		}
		v, e := s.Observation(ctx, o.ID)
		must(t, e)
		if behaviors[i] == "timeout" && v.ErrorCategory != "ANALYSIS_OUTCOME_UNKNOWN" {
			t.Fatal("timeout was replayed or misclassified", v)
		}
		if behaviors[i] != "timeout" && v.ExtractionStatus != "COMPLETE" {
			t.Fatal(v)
		}
		first, e := q.R.HGet(ctx, q.Prefix+"first_start_ms", o.ID).Int64()
		must(t, e)
		rows = append(rows, map[string]any{"behavior": behaviors[i], "created_ms": o.ObservedAt.UnixMilli(), "first_call_ms": first, "completed_observed_ms": done[o.ID], "calls": calls, "state": v.ExtractionStatus, "code": v.ErrorCategory})
	}
	raw := map[string]any{"workers": 2, "sample_interval_ms": 10, "provider_ms": map[string]int{"fast": 10, "slow": 120, "429_retry": 60, "execution_timeout": 2000}, "elapsed_ms": time.Since(started).Milliseconds(), "observed_terminal_per_second": 6 / time.Since(started).Seconds(), "peak_resident_sampled": peak, "rows": rows}
	must(t, os.WriteFile(filepath.Join(filepath.Dir(os.Getenv("CAMPUS_ADVERSARIAL_EVIDENCE")), "load.json"), []byte(d.JSON(raw)), 0600))
	q.R.Set(ctx, q.Prefix+"stop", 1, 0)
	a.stop(false)
	b.stop(false)
}

func TestAdversarialKillWorkerAtPaidBoundaries(t *testing.T) {
	adversarialEnabled(t)
	for _, mode := range []string{"BEFORE_EXTERNAL", "DURING", "BEFORE_CACHE", "BEFORE_SQL", "BEFORE_SQL_NULL", "AFTER_SQL"} {
		t.Run(mode, func(t *testing.T) {
			ctx, s, q, u, _ := setup(t)
			q.Partitioned = true
			q.ResolveOwner = s.TaskOwner
			must(t, q.Init(ctx))
			o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Kill", Title: mode, JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go 可靠任务 " + mode, FetchStatus: "SUCCESS"})
			must(t, e)
			task, e := s.BindAnalysis(ctx, p.NewTask("ANALYZE", o.ID), d.ProcessingVersion("", d.ParserVersion, o.ParserVersion))
			must(t, e)
			_, e = s.DB.ExecContext(ctx, "UPDATE outbox SET sent=TRUE WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id')) IN (?,?)", o.ID, o.JobID)
			must(t, e)
			must(t, q.Publish(ctx, task))
			c := startAdversarial(t, adversarialConfig{Role: "worker", Prefix: q.Prefix, Owner: u, Entity: o.ID, Mode: mode})
			barrier := q.Prefix + "stage"
			if mode == "BEFORE_CACHE" || mode == "AFTER_SQL" {
				barrier = q.Prefix + "blocked"
			}
			adversarialWait(t, ctx, q.R, barrier)
			var tx *sql.Tx
			if strings.HasPrefix(mode, "BEFORE_SQL") {
				tx, e = s.DB.BeginTx(ctx, nil)
				must(t, e)
				defer tx.Rollback()
				var id string
				must(t, tx.QueryRowContext(ctx, "SELECT id FROM jobs WHERE id=? FOR UPDATE", o.JobID).Scan(&id))
				q.R.Set(ctx, q.Prefix+"release", 1, 0)
				adversarialWait(t, ctx, q.R, q.Prefix+"analysis:cache-v2:"+task.ProcessingVersion+":"+d.Hash(o.Text))
			}
			c.stop(true)
			if status, ok := c.cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("worker was not SIGKILLed", c.cmd.ProcessState)
			}
			if tx != nil {
				must(t, tx.Commit())
			}
			before := q.R.Get(ctx, q.Prefix+"calls").Val()
			if mode == "BEFORE_EXTERNAL" {
				if before != "" {
					t.Fatal("called before barrier", before)
				}
			} else if before != "1" {
				t.Fatal("unexpected calls before kill", before)
			}
			q.R.Del(ctx, q.Prefix+"blocked", q.Prefix+"stage", q.Prefix+"release", q.Prefix+"stop")
			restarted := startAdversarial(t, adversarialConfig{Role: "worker", Prefix: q.Prefix, Owner: u, Entity: o.ID, Mode: "NORMAL"})
			end := time.Now().Add(12 * time.Second)
			expected := "ANALYSIS_OUTCOME_UNKNOWN"
			if strings.HasPrefix(mode, "BEFORE_SQL") || mode == "AFTER_SQL" {
				expected = "COMPLETE"
			}
			for time.Now().Before(end) {
				obs, e := s.Observation(ctx, o.ID)
				must(t, e)
				if (obs.ErrorCategory == expected || obs.ExtractionStatus == expected) && !q.R.HExists(ctx, q.Prefix+"ready:ANALYZE:tasks", task.ID).Val() {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			obs, e := s.Observation(ctx, o.ID)
			must(t, e)
			if (obs.ErrorCategory != expected && obs.ExtractionStatus != expected) || q.R.HExists(ctx, q.Prefix+"ready:ANALYZE:tasks", task.ID).Val() {
				t.Fatal("recovery outcome", mode, obs.ExtractionStatus, obs.ErrorCategory)
			}
			if after := q.R.Get(ctx, q.Prefix+"calls").Val(); after != before {
				t.Fatal("unknown or saved call replayed", mode, before, after)
			}
			q.R.Set(ctx, q.Prefix+"stop", 1, 0)
			restarted.stop(false)
			t.Logf("mode=%s original_pid=%d restart_pid=%d calls=%s outcome=%s", mode, c.cmd.Process.Pid, restarted.cmd.Process.Pid, before, expected)
		})
	}
}

func TestAdversarialLegacyUnindexedQuotaAcrossProcesses(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, q, u, _ := setup(t)
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Legacy Capacity", Title: "Go", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go", FetchStatus: "SUCCESS"})
	must(t, e)
	for batch := 0; batch < 40; batch++ {
		args := []any{}
		values := []string{}
		for i := 0; i < 500; i++ {
			v := p.NewTask("ASSESS", o.JobID)
			args = append(args, v.ID, d.JSON(v), v.CreatedAt)
			values = append(values, "(?,?,?)")
		}
		_, e = s.DB.ExecContext(ctx, "INSERT INTO outbox(id,body,created_at) VALUES "+strings.Join(values, ","), args...)
		must(t, e)
	}
	gate := q.Prefix + "quota-go"
	children := []*adversarialChild{}
	for i := 0; i < 4; i++ {
		children = append(children, startAdversarial(t, adversarialConfig{Role: "outbox", Prefix: q.Prefix, Owner: u, Entity: o.JobID, Gate: gate}))
	}
	time.Sleep(100 * time.Millisecond)
	q.R.Set(ctx, gate, 1, 0)
	for _, c := range children {
		c.stop(false)
	}
	if n := q.R.Get(ctx, q.Prefix+"accepted").Val(); n != "" {
		t.Fatal("unindexed legacy tasks bypass owner admission", n)
	}
}
