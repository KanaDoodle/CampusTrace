package integration

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

type narrationFixture struct {
	calls        atomic.Int64
	text         string
	chunks       int
	firstFails   bool
	ignoreCancel bool
	release      chan struct{}
	stopped      chan struct{}
	stopOnce     sync.Once
}

func (*narrationFixture) Identity() string     { return "synthetic-narration-v1" }
func (*narrationFixture) Label() string        { return "fixture-no-model" }
func (*narrationFixture) EvidenceKind() string { return "fixture-no-model" }
func (f *narrationFixture) Generate(ctx context.Context, user string, messages []agent.Message, emit func(string) error) (agent.NarrationUsage, error) {
	n := f.calls.Add(1)
	if f.text != "" {
		return agent.NarrationUsage{}, emit(f.text)
	}
	defer func() {
		if f.stopped != nil {
			f.stopOnce.Do(func() { close(f.stopped) })
		}
	}()
	chunks := f.chunks
	if chunks == 0 {
		chunks = 8
	}
	for k := 0; k < chunks; k++ {
		if err := emit(strings.Repeat("无模型测试说明。", 30)); err != nil && !f.ignoreCancel {
			return agent.NarrationUsage{}, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		if f.ignoreCancel {
			<-timer.C
		} else {
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return agent.NarrationUsage{}, ctx.Err()
			}
		}
	}
	if f.firstFails && n == 1 {
		return agent.NarrationUsage{}, io.ErrUnexpectedEOF
	}
	if f.release != nil {
		if f.ignoreCancel {
			<-f.release
		} else {
			select {
			case <-f.release:
			case <-ctx.Done():
				return agent.NarrationUsage{}, ctx.Err()
			}
		}
	}
	return agent.NarrationUsage{PromptTokens: 100, CompletionTokens: 20}, nil
}
func waitNarration(t *testing.T, store *p.Store, user, id string, check func(p.AgentExecution) bool) p.AgentExecution {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for {
		v, err := store.AgentExecution(context.Background(), user, id)
		must(t, err)
		if check(v) {
			return v
		}
		if time.Now().After(until) {
			t.Fatalf("narration did not settle: %+v", v)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func narrationParent(t *testing.T, ctx context.Context, store *p.Store, user string) (p.AgentExecution, *checkpointTools) {
	t.Helper()
	tools := &checkpointTools{}
	v, err := (agent.SkillRunner{Store: store, Tools: tools}).Start(ctx, user, agent.SkillInput{SkillID: "daily-review", RequestKey: d.ID()})
	must(t, err)
	if v.State != "COMPLETED" || v.SourceIdentity == "" {
		t.Fatal(v)
	}
	return v, tools
}
func narrationInput(t *testing.T, r *agent.NarrationRunner, ctx context.Context, user, parent string) agent.NarrationRequest {
	t.Helper()
	review, err := r.Preview(ctx, user, parent, "")
	must(t, err)
	return agent.NarrationRequest{RequestKey: d.ID(), ReviewHash: review.ReviewHash, Confirm: true}
}

func TestNarrationReviewReplayAndReadOnlyStreamReconnect(t *testing.T) {
	ctx, s, q, user, _ := setup(t)
	parent, tools := narrationParent(t, ctx, s, user)
	f := &narrationFixture{release: make(chan struct{})}
	runner := agent.NewNarrationRunner(ctx, s, f)
	defer runner.Close()
	input := narrationInput(t, runner, ctx, user, parent.ID)
	if f.calls.Load() != 0 {
		t.Fatal("preview called a provider")
	}
	bad := input
	bad.Confirm = false
	if _, err := runner.Start(ctx, user, parent.ID, bad); !errors.Is(err, p.ErrValidation) {
		t.Fatal(err)
	}
	v, err := runner.Start(ctx, user, parent.ID, input)
	must(t, err)
	waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.Narration != nil && len(v.Narration.Draft) > 0 })
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			same, e := runner.Start(ctx, user, parent.ID, input)
			if e != nil || same.ID != v.ID {
				t.Errorf("duplicate create: %s %v", same.ID, e)
			}
		}()
	}
	wg.Wait()
	a := &transport.API{Store: s, Queue: q, Tools: &agent.Tools{Store: s, Queue: q}, Auth: auth.Service{Store: s, Secret: []byte(controlTestSecret)}, Narrations: runner}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	token, err := a.Auth.Token(user)
	must(t, err)
	request, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/agent/executions/"+v.ID+"/stream", nil)
	must(t, err)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	must(t, err)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	seen := false
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data:") {
			seen = true
			break
		}
	}
	response.Body.Close()
	if !seen {
		t.Fatal("no durable stream snapshot")
	}
	// A new observer and ordinary reads cannot restart or cancel generation.
	read := controlRequest(ctx, server.URL, token, "/api/agent/executions/"+v.ID, nil)
	if read.Status != 200 {
		t.Fatal(read)
	}
	response, err = http.DefaultClient.Do(request.Clone(ctx))
	must(t, err)
	response.Body.Close()
	if f.calls.Load() != 1 {
		t.Fatal("reattach or duplicate create called model again", f.calls.Load())
	}
	close(f.release)
	done := waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.State == "COMPLETED" })
	if done.Narration.ProviderState != "COMPLETED" || done.Narration.EvidenceKind != "fixture-no-model" || done.Answer == "" {
		t.Fatal(done)
	}
	if len(tools.calls) != 4 {
		t.Fatal("generation reran tools", tools.calls)
	}
	other, err := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, err)
	otherToken, err := a.Auth.Token(other)
	must(t, err)
	for _, path := range []string{"/api/agent/executions/" + v.ID, "/api/agent/executions/" + v.ID + "/stream"} {
		reply := controlRequest(ctx, server.URL, otherToken, path, nil)
		if reply.Status != 404 {
			t.Fatal("cross-owner read", reply)
		}
	}
	if reply := controlRequest(ctx, server.URL, otherToken, "/api/agent/executions/"+v.ID+"/cancel", struct{}{}); reply.Status != 404 {
		t.Fatal("cross-owner cancel", reply)
	}
}
func TestNarrationUnknownOutcomeNeedsReviewAndAttemptReceipt(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{firstFails: true}
	runner := agent.NewNarrationRunner(ctx, s, f)
	defer runner.Close()
	input := narrationInput(t, runner, ctx, user, parent.ID)
	v, err := runner.Start(ctx, user, parent.ID, input)
	must(t, err)
	paused := waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.State == "PAUSED" })
	if paused.Narration.ProviderState != "UNKNOWN" {
		t.Fatal(paused)
	}
	_, err = runner.Start(ctx, user, parent.ID, input)
	must(t, err)
	_, err = runner.Resume(ctx, user, v.ID, input)
	must(t, err)
	if f.calls.Load() != 1 {
		t.Fatal("receipt replay called model")
	}
	retry := input
	retry.RequestKey = d.ID()
	if _, err = runner.Resume(ctx, user, v.ID, retry); !errors.Is(err, p.ErrConflict) {
		t.Fatal("unknown retry was not blocked", err)
	}
	retry.RetryUnknown = true
	_, err = runner.Resume(ctx, user, v.ID, retry)
	must(t, err)
	waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.State == "COMPLETED" })
	_, err = runner.Resume(ctx, user, v.ID, retry)
	must(t, err)
	_, err = runner.Resume(ctx, user, v.ID, input)
	must(t, err)
	if f.calls.Load() != 2 {
		t.Fatal("attempt receipt replay", f.calls.Load())
	}
	if _, _, err = s.ClaimAgentExecution(ctx, user, v.ID); !errors.Is(err, p.ErrConflict) {
		t.Fatal("generic skill endpoint acquired narration authority", err)
	}
}
func TestNarrationCancelAcrossAPIsFencesLateCompletion(t *testing.T) {
	ctx, s, q, user, _ := setup(t)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{release: make(chan struct{}), ignoreCancel: true, stopped: make(chan struct{})}
	runner := agent.NewNarrationRunner(ctx, s, f)
	input := narrationInput(t, runner, ctx, user, parent.ID)
	v, err := runner.Start(ctx, user, parent.ID, input)
	must(t, err)
	waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.Narration != nil && len(v.Narration.Draft) > 0 })
	// Other API instance has no local active map for this execution.
	a := &transport.API{Store: s, Queue: q, Auth: auth.Service{Store: s, Secret: []byte(controlTestSecret)}}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	token, err := a.Auth.Token(user)
	must(t, err)
	if reply := controlRequest(ctx, server.URL, token, "/api/agent/executions/"+v.ID+"/cancel", struct{}{}); reply.Status != 200 {
		t.Fatal(reply)
	}
	close(f.release)
	runner.Close()
	cancelled, err := s.AgentExecution(ctx, user, v.ID)
	must(t, err)
	if cancelled.State != "CANCELLED" || cancelled.Answer != "" {
		t.Fatal("late model result resurrected task", cancelled)
	}
}
func TestNarrationShutdownRecoveryAndStaleReview(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{release: make(chan struct{})}
	runner := agent.NewNarrationRunner(ctx, s, f)
	input := narrationInput(t, runner, ctx, user, parent.ID)
	v, err := runner.Start(ctx, user, parent.ID, input)
	must(t, err)
	waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.Narration != nil && len(v.Narration.Draft) > 0 })
	runner.Close()
	paused, err := s.AgentExecution(ctx, user, v.ID)
	must(t, err)
	if paused.State != "PAUSED" || !paused.CanResume || paused.Narration.ProviderState != "UNKNOWN" {
		t.Fatal(paused)
	}
	f2 := &narrationFixture{}
	restarted := agent.NewNarrationRunner(ctx, s, f2)
	defer restarted.Close()
	_, err = restarted.Start(ctx, user, parent.ID, input)
	must(t, err)
	if f2.calls.Load() != 0 {
		t.Fatal("startup replayed model")
	}
	retry := input
	retry.RequestKey = d.ID()
	retry.RetryUnknown = true
	_, err = restarted.Resume(ctx, user, v.ID, retry)
	must(t, err)
	waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.State == "COMPLETED" })
	must(t, s.SaveProfile(ctx, user, d.Profile{Degree: "MASTER", Skills: []string{"Go"}}))
	if _, err = restarted.Start(ctx, user, parent.ID, agent.NarrationRequest{RequestKey: d.ID(), ReviewHash: input.ReviewHash, Confirm: true}); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("old reviewed facts reused", err)
	}
}
func TestNarrationForgetErasesDraftAndRejectsLateResult(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	memory, err := s.SaveMemory(ctx, user, p.MemoryInput{Kind: "PREFERENCE", Content: "主投 Go 后端"})
	must(t, err)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{release: make(chan struct{}), ignoreCancel: true}
	runner := agent.NewNarrationRunner(ctx, s, f)
	input := narrationInput(t, runner, ctx, user, parent.ID)
	v, err := runner.Start(ctx, user, parent.ID, input)
	must(t, err)
	waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.Narration != nil && len(v.Narration.Draft) > 0 })
	must(t, s.ForgetMemory(ctx, user, memory.ID, memory.Version))
	close(f.release)
	runner.Close()
	stored, err := s.AgentExecution(ctx, user, v.ID)
	must(t, err)
	if stored.State != "STALE" || stored.Narration != nil || stored.Answer != "" || len(stored.Input) != 0 && string(stored.Input) != "null" {
		t.Fatal("forgotten narration survived", stored)
	}
}

func TestNarrationBeforeDispatchInterruptionRemainsResumable(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{}
	runner := agent.NewNarrationRunner(ctx, s, f)
	defer runner.Close()
	runner.Timeout = time.Nanosecond // expire before the first SQL read in run
	input := narrationInput(t, runner, ctx, user, parent.ID)
	v, err := runner.Start(ctx, user, parent.ID, input)
	must(t, err)
	paused := waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.State != "RUNNING" })
	if paused.State != "PAUSED" || !paused.CanResume || paused.Narration.ProviderState != "NOT_DISPATCHED" || f.calls.Load() != 0 {
		t.Fatalf("pre-dispatch timeout must not invalidate reviewed source: %+v", paused)
	}
}

func TestNarrationRateLimitPreservesReasonWithoutDispatch(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{}
	runner := agent.NewNarrationRunner(ctx, s, f)
	defer runner.Close()
	runner.Allow = func(context.Context, string) (bool, error) { return false, nil }
	input := narrationInput(t, runner, ctx, user, parent.ID)
	v, err := runner.Start(ctx, user, parent.ID, input)
	must(t, err)
	paused := waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.State == "PAUSED" })
	if paused.Narration.Failure != "RATE_LIMIT" || paused.Narration.ProviderState != "NOT_DISPATCHED" || f.calls.Load() != 0 {
		t.Fatalf("rate-limit cause lost: %+v", paused.Narration)
	}
}

func TestNarrationSessionRevocationClosesObserverWithoutReplayingGeneration(t *testing.T) {
	ctx, s, q, user, _ := setup(t)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{release: make(chan struct{})}
	runner := agent.NewNarrationRunner(ctx, s, f)
	defer runner.Close()
	v, err := runner.Start(ctx, user, parent.ID, narrationInput(t, runner, ctx, user, parent.ID))
	must(t, err)
	sessions := auth.Sessions{Redis: q.R, Prefix: q.Prefix}
	credential, _, err := sessions.Create(ctx, user, false)
	must(t, err)
	a := &transport.API{Store: s, Queue: q, Narrations: runner, Auth: auth.Service{Store: s, Secret: []byte(controlTestSecret)}}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	observe, stop := context.WithCancel(ctx)
	defer stop()
	request, err := http.NewRequestWithContext(observe, http.MethodGet, server.URL+"/api/agent/executions/"+v.ID+"/stream", nil)
	must(t, err)
	request.AddCookie(&http.Cookie{Name: "campustrace-session", Value: credential})
	response, err := http.DefaultClient.Do(request)
	must(t, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("observer was not authenticated: %d", response.StatusCode)
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, response.Body); close(done) }()
	must(t, sessions.Revoke(ctx, credential))
	select {
	case <-done:
	case <-time.After(750 * time.Millisecond):
		t.Fatal("revoked session kept receiving private narration snapshots")
	}
	close(f.release)
	waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.State == "COMPLETED" })
	if f.calls.Load() != 1 {
		t.Fatal("revoking an observer replayed inference", f.calls.Load())
	}
}

func TestNarrationRedactionExpansionCannotExceedSnapshotLimit(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{text: strings.Repeat("x", 3000)}
	runner := agent.NewNarrationRunner(ctx, s, f)
	defer runner.Close()
	review, err := runner.Preview(ctx, user, parent.ID, "x")
	must(t, err)
	v, err := runner.Start(ctx, user, parent.ID, agent.NarrationRequest{RequestKey: d.ID(), ReviewHash: review.ReviewHash, Confirm: true, MaskName: "x"})
	must(t, err)
	stored := waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.State != "RUNNING" })
	if stored.State != "PAUSED" || stored.Answer != "" || len(stored.Narration.Draft) > 32768 || stored.Narration.Failure != "OUTPUT_LIMIT" {
		t.Fatalf("redaction bypassed snapshot cap: state=%s answer_bytes=%d draft_bytes=%d failure=%s", stored.State, len(stored.Answer), len(stored.Narration.Draft), stored.Narration.Failure)
	}
}

func TestNarrationMemoryEditDuringCheckpointDoesNotStrandRunningLease(t *testing.T) {
	ctx, s, _, user, _ := setup(t)
	parent, _ := narrationParent(t, ctx, s, user)
	f := &narrationFixture{chunks: 40, stopped: make(chan struct{})}
	runner := agent.NewNarrationRunner(ctx, s, f)
	defer runner.Close()
	v, err := runner.Start(ctx, user, parent.ID, narrationInput(t, runner, ctx, user, parent.ID))
	must(t, err)
	waitNarration(t, s, user, v.ID, func(v p.AgentExecution) bool { return v.Narration.Draft != "" })
	_, err = s.SaveMemory(ctx, user, p.MemoryInput{Kind: "PREFERENCE", Content: "本次改投基础架构"})
	must(t, err)
	select {
	case <-f.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("source change did not stop generation at checkpoint")
	}
	runner.Close()
	stored, err := s.AgentExecution(ctx, user, v.ID)
	must(t, err)
	if stored.State != "STALE" || stored.LeaseUntil != nil || stored.Answer != "" || stored.Narration.Draft != "" {
		t.Fatalf("changed memory stranded a lease: state=%s lease=%v draft_bytes=%d", stored.State, stored.LeaseUntil, len(stored.Narration.Draft))
	}
}
