package integration

import (
	"context"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"testing"
	"time"
)

func modelPolicy() p.ModelPoolPolicy {
	return p.ModelPoolPolicy{ID: "test-model-" + d.ID(), Capacity: 1, QueueLimit: 4, OwnerQueueLimit: 2, DailyLimit: 2, LeaseMS: 300}
}
func grantModel(t *testing.T, ctx context.Context, s *p.Store, policy p.ModelPoolPolicy, user string) p.ModelTicket {
	t.Helper()
	v, e := s.EnqueueModelTicket(ctx, policy.ID, user, 2*time.Second)
	must(t, e)
	v, e = s.PollModelTicket(ctx, v.ID, user)
	must(t, e)
	if v.State != "GRANTED" {
		t.Fatal("ticket not granted", v)
	}
	return v
}
func TestModelControlGlobalTurnsAndAtomicBudget(t *testing.T) {
	ctx, s, _, a, _ := setup(t)
	_, _, _, b, _ := setup(t)
	policy := modelPolicy()
	must(t, s.EnsureModelPool(ctx, policy))
	first := grantModel(t, ctx, s, policy, a)
	_, e := s.BeginModelCall(ctx, first, d.Hash("input a"), d.Hash("model"), "", time.Second, 0)
	must(t, e)
	nextA, e := s.EnqueueModelTicket(ctx, policy.ID, a, 2*time.Second)
	must(t, e)
	nextB, e := s.EnqueueModelTicket(ctx, policy.ID, b, 2*time.Second)
	must(t, e)
	v, e := s.PollModelTicket(ctx, nextA.ID, a)
	must(t, e)
	if v.State != "QUEUED" {
		t.Fatal("exceeded global capacity", v)
	}
	must(t, s.FinishModelCall(ctx, first, "RESPONSE_RECEIVED", "OK"))
	v, e = s.PollModelTicket(ctx, nextA.ID, a)
	must(t, e)
	if v.State != "QUEUED" {
		t.Fatal("bulk owner skipped other owner's turn", v)
	}
	nextB, e = s.PollModelTicket(ctx, nextB.ID, b)
	must(t, e)
	if nextB.State != "GRANTED" {
		t.Fatal(nextB)
	}
	_, e = s.BeginModelCall(ctx, nextB, d.Hash("input b"), d.Hash("model"), "", time.Second, 0)
	must(t, e)
	must(t, s.FinishModelCall(ctx, nextB, "REJECTED", "429"))
	nextA, e = s.PollModelTicket(ctx, nextA.ID, a)
	must(t, e)
	_, e = s.BeginModelCall(ctx, nextA, d.Hash("input a2"), d.Hash("model"), "", time.Second, 0)
	if !errors.Is(e, p.ErrModelBudget) {
		t.Fatal("global budget exceeded", e)
	}
	var count int
	must(t, s.DB.QueryRowContext(ctx, "SELECT calls FROM match_daily_usage WHERE user_id=?", a).Scan(&count))
	if count != 1 {
		t.Fatal("failed global reservation consumed user quota", count)
	}
	must(t, s.CancelModelTicket(ctx, nextA))
	stats, e := s.ModelPoolStatus(ctx, policy.ID)
	must(t, e)
	if stats.AllocatedToday != 2 || stats.Active != 0 || stats.Waiting != 0 {
		t.Fatal(stats)
	}
	if e = s.RenewModelCall(ctx, first); !errors.Is(e, p.ErrModelLease) {
		t.Fatal("finished lease renewed", e)
	}
	different := policy
	different.Capacity = 2
	if e = s.EnsureModelPool(ctx, different); !errors.Is(e, p.ErrModelPoolConfig) {
		t.Fatal("conflicting instance policy accepted", e)
	}
}
func TestModelControlUnknownReviewIsVersionedAndRefundsOnce(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	_, _, _, other, _ := setup(t)
	policy := modelPolicy()
	must(t, s.EnsureModelPool(ctx, policy))
	ticket := grantModel(t, ctx, s, policy, u)
	_, e := s.BeginModelCall(ctx, ticket, d.Hash("first"), d.Hash("model"), "", 200*time.Millisecond, 50*time.Millisecond)
	must(t, e)
	must(t, s.FinishModelCall(ctx, ticket, "UNKNOWN", "TIMEOUT"))
	if e = s.CheckModelCallReview(ctx, u, policy.ID, d.Hash("different input")); !errors.Is(e, p.ErrModelReview) {
		t.Fatal("unknown bypassed by changing input", e)
	}
	calls, e := s.ModelCalls(ctx, u, policy.ID)
	must(t, e)
	if len(calls) != 1 || calls[0].State != "UNKNOWN" {
		t.Fatal(calls)
	}
	review := p.ModelCallReview{Version: calls[0].Version, Decision: "NOT_SENT", Verified: true, Note: "已核对合成提供商记录，该请求没有执行。"}
	if _, e = s.ReviewModelCall(ctx, other, policy.ID, ticket.ID, review); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("cross-user review", e)
	}
	bad := review
	bad.Version--
	if _, e = s.ReviewModelCall(ctx, u, policy.ID, ticket.ID, bad); !errors.Is(e, p.ErrConflict) {
		t.Fatal("stale review accepted", e)
	}
	result, e := s.ReviewModelCall(ctx, u, policy.ID, ticket.ID, review)
	must(t, e)
	again, e := s.ReviewModelCall(ctx, u, policy.ID, ticket.ID, review)
	must(t, e)
	if result.Version != again.Version || again.State != "REVIEWED_NOT_SENT" {
		t.Fatal("review replay changed state", result, again)
	}
	changed := review
	changed.Decision = "USED"
	if _, e = s.ReviewModelCall(ctx, u, policy.ID, ticket.ID, changed); !errors.Is(e, p.ErrConflict) {
		t.Fatal("review changed after refund", e)
	}
	stats, e := s.ModelPoolStatus(ctx, policy.ID)
	must(t, e)
	var used int
	must(t, s.DB.QueryRowContext(ctx, "SELECT calls FROM match_daily_usage WHERE user_id=?", u).Scan(&used))
	if used != 0 || stats.AllocatedToday != 0 {
		t.Fatal("quota not returned once", used, stats)
	}
	if stats.Active != 1 || stats.Quarantined != 1 {
		t.Fatal("review prematurely freed uncertain execution", stats)
	}
	time.Sleep(300 * time.Millisecond)
	stats, e = s.ModelPoolStatus(ctx, policy.ID)
	must(t, e)
	if stats.Active != 0 {
		t.Fatal("quarantine never expired", stats)
	}
	if e = s.CheckModelCallReview(ctx, u, policy.ID, d.Hash("next")); e != nil {
		t.Fatal(e)
	}
}
func TestModelControlQueueBoundsAndExpiredFence(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	_, _, _, v, _ := setup(t)
	policy := modelPolicy()
	must(t, s.EnsureModelPool(ctx, policy))
	a, e := s.EnqueueModelTicket(ctx, policy.ID, u, time.Second)
	must(t, e)
	a2, e := s.EnqueueModelTicket(ctx, policy.ID, u, time.Second)
	must(t, e)
	if _, e = s.EnqueueModelTicket(ctx, policy.ID, u, time.Second); !errors.Is(e, p.ErrModelQueueFull) {
		t.Fatal(e)
	}
	b1, e := s.EnqueueModelTicket(ctx, policy.ID, v, time.Second)
	must(t, e)
	b2, e := s.EnqueueModelTicket(ctx, policy.ID, v, time.Second)
	must(t, e)
	if _, e = s.EnqueueModelTicket(ctx, policy.ID, d.ID(), time.Second); e == nil {
		t.Fatal("invalid owner accepted")
	}
	for _, queued := range []p.ModelTicket{a, a2, b1, b2} {
		must(t, s.CancelModelTicket(ctx, queued))
	}
	a = grantModel(t, ctx, s, policy, u)
	_, e = s.DB.ExecContext(ctx, "UPDATE model_resource_tickets SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE id=?", a.ID)
	must(t, e)
	if _, e = s.BeginModelCall(ctx, a, d.Hash("stale"), d.Hash("model"), "", time.Second, 0); !errors.Is(e, p.ErrModelLease) {
		t.Fatal("expired grant started a call", e)
	}
	stats, e := s.ModelPoolStatus(ctx, policy.ID)
	must(t, e)
	if stats.Active != 0 || stats.AllocatedToday != 0 {
		t.Fatal(stats)
	}
}

func TestModelControlMetricsDetectExpiredCallWithoutMutatingLedger(t *testing.T) {
	ctx, s, _, u, _ := setup(t)
	policy := modelPolicy()
	must(t, s.EnsureModelPool(ctx, policy))
	ticket := grantModel(t, ctx, s, policy, u)
	_, e := s.BeginModelCall(ctx, ticket, d.Hash("synthetic"), d.Hash("model"), "", time.Second, 0)
	must(t, e)
	_, e = s.DB.ExecContext(ctx, "UPDATE model_resource_tickets SET lease_until=DATE_SUB(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE id=?", ticket.ID)
	must(t, e)
	stats, e := s.ModelPoolMetrics(ctx, policy.ID)
	must(t, e)
	var state string
	must(t, s.DB.QueryRowContext(ctx, "SELECT state FROM model_call_ledger WHERE id=?", ticket.ID).Scan(&state))
	if stats.Unknown != 1 || stats.AllocatedToday != 1 || state != "STARTED" {
		t.Fatal("scrape missed failure or mutated business state", stats, state)
	}
}
