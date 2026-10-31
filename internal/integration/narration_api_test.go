package integration

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/inferencebridge"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestNarrationAPIRecoveryAndBackendChangeBoundary(t *testing.T) {
	ctx, store, _, owner, _ := setup(t)
	parent, _ := narrationParent(t, ctx, store, owner)
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 8; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%s\"}}]}\n\n", strings.Repeat("无模型 API 依据说明。", 12))
			w.(http.Flusher).Flush()
			time.Sleep(25 * time.Millisecond)
		}
		if n == 1 {
			return
		} // An HTTP disconnect without finish or [DONE].
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20}}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	oldClient, err := inferencebridge.NewAPI(analysis.NewChat(server.URL+"/chat/completions", "synthetic", "old-model", 1), "fixture-no-model")
	must(t, err)
	defer oldClient.Close()
	oldRunner := agent.NewNarrationRunner(ctx, store, oldClient)
	defer oldRunner.Close()
	input := narrationInput(t, oldRunner, ctx, owner, parent.ID)
	v, err := oldRunner.Start(ctx, owner, parent.ID, input)
	must(t, err)
	paused := waitNarration(t, store, owner, v.ID, func(v p.AgentExecution) bool { return v.State == "PAUSED" })
	if paused.Narration.ProviderState != "UNKNOWN" || paused.Narration.Draft == "" {
		t.Fatal("partial API response lost or marked complete", paused)
	}
	// An application restart reuses the same endpoint/model identity. Reads and
	// create receipt replays must never automatically dispatch another call.
	oldRunner.Close()
	restarted := agent.NewNarrationRunner(ctx, store, oldClient)
	defer restarted.Close()
	_, err = store.AgentExecution(ctx, owner, v.ID)
	must(t, err)
	replay, err := restarted.Start(ctx, owner, parent.ID, input)
	must(t, err)
	if replay.ID != v.ID || calls.Load() != 1 {
		t.Fatal("restart/read/create replay dispatched provider")
	}

	newClient, err := inferencebridge.NewAPI(analysis.NewChat(server.URL+"/chat/completions", "synthetic", "new-model", 1), "fixture-no-model")
	must(t, err)
	defer newClient.Close()
	changed := agent.NewNarrationRunner(ctx, store, newClient)
	defer changed.Close()
	newInput := narrationInput(t, changed, ctx, owner, parent.ID)
	newInput.RetryUnknown = true
	if newInput.ReviewHash == input.ReviewHash {
		t.Fatal("provider change did not invalidate consent hash")
	}
	if _, err := changed.Resume(ctx, owner, v.ID, newInput); !errors.Is(err, p.ErrStaleInput) {
		t.Fatal("old approval silently retargeted to new model", err)
	}
	read, err := store.AgentExecution(ctx, owner, v.ID)
	must(t, err)
	if read.Narration.Draft != paused.Narration.Draft || calls.Load() != 1 {
		t.Fatal("backend change erased saved draft or dispatched")
	}

	retry := input
	retry.RequestKey = d.ID()
	if _, err := restarted.Resume(ctx, owner, v.ID, retry); !errors.Is(err, p.ErrConflict) {
		t.Fatal("unknown outcome retried without acknowledgment", err)
	}
	retry.RetryUnknown = true
	_, err = restarted.Resume(ctx, owner, v.ID, retry)
	must(t, err)
	_, err = restarted.Resume(ctx, owner, v.ID, retry)
	must(t, err)
	done := waitNarration(t, store, owner, v.ID, func(v p.AgentExecution) bool { return v.State == "COMPLETED" })
	if calls.Load() != 2 || done.Narration.PromptTokens != 100 || done.Narration.CompletionTokens != 20 {
		t.Fatal("API retry receipt or usage failed", calls.Load(), done)
	}
	// A fresh reviewed child can explicitly use the changed provider; the old
	// completed execution remains readable and is not converted into a new call.
	fresh, err := changed.Start(ctx, owner, parent.ID, newInput)
	must(t, err)
	if fresh.ID == v.ID {
		t.Fatal("changed provider reused old task")
	}
	waitNarration(t, store, owner, fresh.ID, func(v p.AgentExecution) bool { return v.State == "COMPLETED" })
	if calls.Load() != 3 {
		t.Fatal("unexpected provider call count", calls.Load())
	}
}
