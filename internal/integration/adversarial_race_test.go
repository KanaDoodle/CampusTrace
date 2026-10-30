package integration

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type pauseReadGate struct {
	driver.Connector
	original         *p.Store
	reached, release chan struct{}
	once             atomic.Bool
}
type pauseReadConn struct {
	driver.Conn
	g *pauseReadGate
}

func (g *pauseReadGate) Connect(ctx context.Context) (driver.Conn, error) {
	c, e := g.Connector.Connect(ctx)
	return &pauseReadConn{c, g}, e
}
func (c *pauseReadConn) BeginTx(ctx context.Context, o driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, o)
}
func (c *pauseReadConn) ExecContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, a)
}
func (c *pauseReadConn) QueryContext(ctx context.Context, q string, a []driver.NamedValue) (driver.Rows, error) {
	if q == "SELECT body FROM match_runs WHERE user_id=? AND id=?" && len(a) == 2 {
		v, e := c.g.original.MatchRun(ctx, a[0].Value.(string), a[1].Value.(string))
		if e == nil && v.ExecutionState == "EXECUTING" && v.State == "RUNNING" && c.g.once.CompareAndSwap(false, true) {
			close(c.g.reached)
			select {
			case <-c.g.release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, a)
}
func TestAdversarialPauseBetweenExecutingAndCall(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, u, token, ids, api, h, m := wholeSetup(t)
	cfg, e := mysql.ParseDSN(os.Getenv("MYSQL_TEST_DSN"))
	must(t, e)
	connector, e := mysql.NewConnector(cfg)
	must(t, e)
	gate := &pauseReadGate{Connector: connector, original: s, reached: make(chan struct{}), release: make(chan struct{})}
	db := sql.OpenDB(gate)
	defer db.Close()
	api.Store = &p.Store{DB: db, Weights: s.Weights}
	runner := api.StartMatchTasks(ctx, 1)
	defer runner.Close()
	run := schedulerSubmit(t, ctx, s, u, token, ids, h)
	select {
	case <-gate.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("execution boundary was not reached")
	}
	current, e := s.MatchRun(ctx, u, run.ID)
	must(t, e)
	_, e = s.ControlMatchRun(ctx, u, run.ID, current.Version, "PAUSE")
	must(t, e)
	close(gate.release)
	stopped := awaitRun(t, s, u, run.ID, "PAUSED")
	if m.calls.Load() != 0 || stopped.ExecutionState != "" {
		t.Fatal("pause caused a call or interruption", stopped, m.calls.Load())
	}
}

func TestAdversarialOrphanLegacyOutboxCannotBlockHealthyOwner(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, q, u, _ := setup(t)
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Orphan", Title: "Go", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go", FetchStatus: "SUCCESS"})
	must(t, e)
	orphan := p.NewTask("ANALYZE", d.ID())
	valid := p.NewTask("ASSESS", o.JobID)
	must(t, s.Tx(ctx, func(tx *sql.Tx) error {
		if e := p.Outbox(ctx, tx, orphan); e != nil {
			return e
		}
		return p.Outbox(ctx, tx, valid)
	}))
	t.Cleanup(func() { s.DB.ExecContext(context.Background(), "DELETE FROM outbox WHERE id=?", orphan.ID) })
	q.Partitioned = true
	q.ResolveOwner = s.TaskOwner
	must(t, q.Init(ctx))
	w := worker(s, q)
	for i := 0; i < 3; i++ {
		if e := w.Dispatch(ctx); e != nil {
			t.Fatal("orphan blocked dispatch", e)
		}
	}
	var sent bool
	must(t, s.DB.QueryRowContext(ctx, "SELECT sent FROM outbox WHERE id=?", valid.ID).Scan(&sent))
	if !sent {
		t.Fatal("healthy task never dispatched")
	}
	child := startAdversarial(t, adversarialConfig{Role: "worker", Prefix: q.Prefix, Mode: "NORMAL"})
	adversarialWait(t, ctx, q.R, q.Prefix+"dlq")
	raw, e := q.R.HGet(ctx, q.Prefix+"dlq", orphan.ID).Result()
	must(t, e)
	var failure pipeline.Failure
	must(t, json.Unmarshal([]byte(raw), &failure))
	if failure.Task.ID != orphan.ID {
		t.Fatal("orphan audit identity changed", failure)
	}
	q.R.Set(ctx, q.Prefix+"stop", 1, 0)
	child.stop(false)
}

func TestAdversarialFullOutboxDoesNotDeadlockDerivedAssessment(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, q, u, _ := setup(t)
	o, e := s.IngestForUser(ctx, u, p.Ingest{Company: "Synthetic Full Derived", Title: "Go", JobType: "FULL_TIME", ExternalID: d.ID(), Text: "Go", FetchStatus: "SUCCESS"})
	must(t, e)
	// The initial assessment has finished while its ANALYZE is still waiting.
	// This is also a valid legacy recovery state: no assessment is pending.
	initial, e := p.One[p.Task](ctx, s.DB, "SELECT body FROM outbox WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.entity_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.task_type'))='ASSESS'", o.JobID)
	must(t, e)
	_, e = s.DB.ExecContext(ctx, "UPDATE outbox SET sent=TRUE WHERE id=?", initial.ID)
	must(t, e)
	must(t, s.Assess(ctx, initial.ID, o.JobID))
	for batch := 0; batch < 40; batch++ {
		args := []any{}
		values := []string{}
		for i := 0; i < 500; i++ {
			v := p.NewTask("ANALYZE", o.ID)
			args = append(args, v.ID, d.JSON(v), v.CreatedAt)
			values = append(values, "(?,?,?)")
		}
		_, e = s.DB.ExecContext(ctx, "INSERT INTO outbox(id,body,created_at) VALUES "+strings.Join(values, ","), args...)
		must(t, e)
	}
	_, e = s.BindAnalysis(ctx, p.NewTask("ANALYZE", o.ID), d.ProcessingVersion("", d.ParserVersion, o.ParserVersion))
	if e != nil {
		t.Fatal("derived ASSESS prevents admission-full ANALYZE from draining", e)
	}
	var pending sql.NullString
	var due sql.NullTime
	must(t, s.DB.QueryRowContext(ctx, "SELECT pending_task_id,next_assess_at FROM job_assessment_schedule WHERE job_id=?", o.JobID).Scan(&pending, &due))
	if pending.Valid || !due.Valid {
		t.Fatal("deferred assessment is not durably due", pending, due)
	}
	q.Partitioned = true
	q.ResolveOwner = s.TaskOwner
	must(t, q.Init(ctx))
	w := worker(s, q)
	must(t, w.Dispatch(ctx))
	must(t, w.Dispatch(ctx))
	n, e := s.ScheduleAssessments(ctx, time.Now().UTC(), 100)
	must(t, e)
	if n != 1 {
		t.Fatal("deferred assessment not re-admitted", n)
	}
	var total int
	must(t, s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox WHERE sent=FALSE").Scan(&total))
	if total > 20000 {
		t.Fatal("assessment bypassed hard capacity", total)
	}
}
