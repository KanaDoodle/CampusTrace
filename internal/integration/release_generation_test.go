package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/pipeline"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"github.com/redis/go-redis/v9"
	"sync/atomic"
	"testing"
	"time"
)

type countAnalysis struct{ calls atomic.Int32 }

func (a *countAnalysis) Analyze(ctx context.Context, o d.Observation, _ string) ([]d.Claim, error) {
	a.calls.Add(1)
	return analysis.Extract(ctx, o.Text)
}
func TestReleaseParserReceiptReconciliation(t *testing.T) {
	ctx, s, q, _, src := setup(t)
	in := ingest(t, ctx, s, src)
	o, err := s.Ingest(ctx, in)
	must(t, err)
	w := worker(s, q)
	a := &countAnalysis{}
	w.Analyzer = a
	w.ParserVersion = "parser-before"
	old := p.NewTask("ANALYZE", o.ID)
	must(t, w.Process(ctx, old))
	w.ParserVersion = "parser-after"
	next := p.NewTask("ANALYZE", o.ID)
	must(t, w.Process(ctx, next))
	if a.calls.Load() != 2 {
		t.Fatal("parser bump blocked by receipt", a.calls.Load())
	}
	cur, err := s.Observation(ctx, o.ID)
	must(t, err)
	want := cur.AnalysisVersion
	generation := cur.ActiveAnalysisGeneration
	cur.AnalysisVersion = "corrupt-old-pointer"
	cur.CurrentAnalysisGeneration = 0
	cur.ApplySignal = "ABSENT"
	_, err = s.DB.ExecContext(ctx, "UPDATE observations SET body=? WHERE id=?", d.JSON(cur), cur.ID)
	must(t, err)
	must(t, w.Process(ctx, next))
	fixed, err := s.Observation(ctx, o.ID)
	must(t, err)
	if fixed.AnalysisVersion != want || fixed.CurrentAnalysisGeneration != generation || fixed.ApplySignal != "PRESENT" {
		t.Fatal("receipt did not reconcile", fixed)
	}
	if a.calls.Load() != 2 {
		t.Fatal("receipt reconciliation reran analyzer")
	}
	// A delayed retry carrying the old identity must not reactivate it.
	w.ParserVersion = "parser-before"
	must(t, w.Process(ctx, old))
	fixed, err = s.Observation(ctx, o.ID)
	must(t, err)
	if fixed.ActiveAnalysisGeneration != generation {
		t.Fatal("old task reactivated")
	}
	es, err := s.Evidence(ctx, o.JobID)
	must(t, err)
	_, current := rules.Current([]d.Observation{fixed}, es)
	if len(es) != 2*len(current) || len(current) == 0 {
		t.Fatal("history lost or current mixed", len(es), len(current))
	}
}
func TestReleaseGenerationClearsDerivedFields(t *testing.T) {
	ctx, s, q, _, src := setup(t)
	in := ingest(t, ctx, s, src)
	in.Text += "\ndeadline: 2027-12-01"
	o, err := s.Ingest(ctx, in)
	must(t, err)
	must(t, worker(s, q).Process(ctx, p.NewTask("ANALYZE", o.ID)))
	task, err := s.BindAnalysis(ctx, p.NewTask("ANALYZE", o.ID), d.ProcessingVersion("no-signals", d.ParserVersion, o.ParserVersion))
	must(t, err)
	pending, err := s.Observation(ctx, o.ID)
	must(t, err)
	es, err := s.Evidence(ctx, o.JobID)
	must(t, err)
	_, selected := rules.Current([]d.Observation{pending}, es)
	if len(selected) != 0 {
		t.Fatal("inactive evidence selected while pending")
	}
	must(t, s.PersistAnalysis(ctx, o.ID, task.ProcessingVersion, []d.Claim{}))
	cur, err := s.Observation(ctx, o.ID)
	must(t, err)
	if cur.ApplySignal != "UNKNOWN" || cur.DeadlineSignal != "" {
		t.Fatal("derived signals retained", cur)
	}
}
func TestReleaseReceiptOwnershipAndBackendFailure(t *testing.T) {
	ctx, s, q, u, src := setup(t)
	o, err := s.Ingest(ctx, ingest(t, ctx, s, src))
	must(t, err)
	tools := &agent.Tools{Store: s, Queue: q}
	pending, err := tools.Propose(ctx, u, "create_application", json.RawMessage(d.JSON(p.CreateArgs{JobID: o.JobID})))
	must(t, err)
	first, err := tools.Confirm(ctx, u, pending.ID)
	must(t, err)
	must(t, q.R.Del(ctx, q.Prefix+"pending:"+pending.ID).Err())
	replay, err := tools.Confirm(ctx, u, pending.ID)
	must(t, err)
	if string(replay) != string(first) {
		t.Fatal("receipt result changed")
	}
	other, err := s.NewUser(ctx, d.ID()+"@receipt.test", "unused")
	must(t, err)
	if value, err := tools.Confirm(ctx, other, pending.ID); !errors.Is(err, p.ErrNotFound) || value != nil {
		t.Fatal("cross-user receipt leaked", string(value), err)
	}
	if _, err := tools.Confirm(ctx, u, d.ID()); !errors.Is(err, p.ErrNotFound) {
		t.Fatal("missing pending not NotFound", err)
	}
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: time.Millisecond * 20})
	defer dead.Close()
	tools.Queue = &pipeline.Queue{R: dead, Prefix: q.Prefix}
	if _, err := tools.Confirm(ctx, u, d.ID()); err == nil || errors.Is(err, p.ErrNotFound) {
		t.Fatal("backend unavailable masked", err)
	}
	replay, err = tools.Confirm(ctx, u, pending.ID)
	must(t, err)
	if string(replay) != string(first) {
		t.Fatal("SQL receipt depended on Redis")
	}
}

func TestReleaseActiveGenerationConsumersAndInputCAS(t *testing.T) {
	ctx, s, q, u, src := setup(t)
	must(t, s.SaveProfile(ctx, u, d.Profile{Degree: "BACHELOR", GraduationYear: 2027, PreferredTypes: []string{"FULL_TIME"}, Skills: []string{"go"}}))
	in := ingest(t, ctx, s, src)
	in.Text += "\ndegree: PHD\ntech: REQUIRED:java"
	o, err := s.Ingest(ctx, in)
	must(t, err)
	old, err := s.BindAnalysis(ctx, p.NewTask("ANALYZE", o.ID), d.ProcessingVersion("old-rules", d.ParserVersion, o.ParserVersion))
	must(t, err)
	oldClaims, err := analysis.Extract(ctx, jd)
	must(t, err)
	must(t, s.PersistAnalysis(ctx, o.ID, old.ProcessingVersion, oldClaims))
	before, err := s.ComputeEvaluation(ctx, u, o.JobID)
	must(t, err)
	next, err := s.BindAnalysis(ctx, p.NewTask("ANALYZE", o.ID), d.ProcessingVersion("new-rules", d.ParserVersion, o.ParserVersion))
	must(t, err)
	if err = s.PersistAssessment(ctx, before); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("generation change bypassed CAS", err)
	}
	newClaims := append([]d.Claim{}, oldClaims...)
	for i := range newClaims {
		if newClaims[i].Type == "EDUCATION_REQUIREMENT" {
			newClaims[i].Value = "PHD"
			newClaims[i].Excerpt = "degree: PHD"
		}
		if newClaims[i].Type == "TECH_STACK" {
			newClaims[i].Value = "REQUIRED:java"
			newClaims[i].Excerpt = "tech: REQUIRED:java"
		}
	}
	must(t, s.PersistAnalysis(ctx, o.ID, next.ProcessingVersion, newClaims))
	must(t, s.PersistAnalysis(ctx, o.ID, old.ProcessingVersion, oldClaims))
	current, err := s.ComputeEvaluation(ctx, u, o.JobID)
	must(t, err)
	if current.Eligibility.Status != "INELIGIBLE" || current.GoFit != "NO_GO_SIGNAL" || current.Ranking.Breakdown["go_fit"] != 0 || current.Ranking.Breakdown["eligibility"] != 0 {
		t.Fatal("consumer used old-generation evidence", current)
	}
	for _, e := range current.Evidence {
		if e.AnalysisVersion != next.ProcessingVersion {
			t.Fatal("mixed generation", e)
		}
	}
	in.Title = "New metadata title"
	in.ObservedAt = time.Now().UTC().Add(-time.Minute)
	_, err = s.Ingest(ctx, in)
	must(t, err)
	if err = s.PersistAssessment(ctx, current); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("metadata/observation change bypassed CAS", err)
	}
	// Same-step equivalent proposals with different IDs/default representations share one pending action.
	m := &reModel{calls: []agent.Call{{ID: "a", Name: "create_application", Args: json.RawMessage(d.JSON(p.CreateArgs{JobID: o.JobID}))}, {ID: "b", Name: "create_application", Args: json.RawMessage(`{"resume_version":"","job_id":"` + o.JobID + `"}`)}}}
	tools := &agent.Tools{Store: s, Queue: q}
	r := &agent.Runtime{Model: m, Tools: tools, MaxModels: 3, MaxTools: 8, Deadline: time.Second * 5, ToolTimeout: time.Second}
	out := r.Run(ctx, u, "dup-actions", "q", nil)
	if out.Executed != 1 {
		t.Fatal("equivalent proposals executed twice", out.Executed)
	}
	keys, err := q.R.Keys(ctx, q.Prefix+"pending:*").Result()
	must(t, err)
	if len(keys) != 1 {
		t.Fatal("duplicate pending keys", len(keys))
	}
}

func TestReleaseBoundTaskCannotChangeImplementation(t *testing.T) {
	ctx, s, q, _, src := setup(t)
	o, err := s.Ingest(ctx, ingest(t, ctx, s, src))
	must(t, err)
	task, err := s.BindAnalysis(ctx, p.NewTask("ANALYZE", o.ID), d.ProcessingVersion("new-implementation", d.ParserVersion, o.ParserVersion))
	must(t, err)
	wrong := worker(s, q)
	if err = wrong.Process(ctx, task); !errors.Is(err, p.ErrProcessingUnavailable) {
		t.Fatal("wrong implementation was allowed", err)
	}
	matching := worker(s, q)
	matching.Version = "new-implementation"
	must(t, matching.Process(ctx, task))
	must(t, wrong.Process(ctx, task)) // a completed receipt can reconcile without executing a mismatched analyzer
}
