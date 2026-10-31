package inferencebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
)

func TestAPINarrationSharesConcurrencyStreamsAndKeepsOwnerLocal(t *testing.T) {
	var calls atomic.Int64
	first := make(chan string, 1)
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer synthetic-key" || r.Header.Get("Accept") != "text/event-stream" {
			t.Error("wrong endpoint or authentication")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["stream"] != true || body["max_tokens"] != float64(512) || body["model"] != "configured-alias" || body["stream_options"].(map[string]any)["include_usage"] != true {
			t.Error("wrong stream configuration", body)
		}
		for _, k := range []string{"tools", "tool_choice", "cache_salt", "user", "invocation_id"} {
			if _, ok := body[k]; ok {
				t.Errorf("unexpected external field %s", k)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fmt.Fprint(w, "data: {\"model\":\"actual-model-revision\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"第一段\",\"reasoning_content\":\"private reasoning\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"第二段\"},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n")
	}))
	defer s.Close()
	shared := analysis.NewChat(s.URL+"/v1/chat/completions", "synthetic-key", "configured-alias", 1)
	shared.OutputTokenLimit = 99
	shared.Allow = func(context.Context) (bool, error) {
		t.Error("global narration quota debited twice")
		return false, nil
	}
	c, err := NewAPI(shared, "fixture-no-model")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	shared.Sem <- struct{}{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		var text strings.Builder
		u, err := c.Generate(context.Background(), "private-owner-id", []agent.Message{{Role: "system", Content: "reviewed"}, {Role: "user", Content: "evidence"}}, func(s string) error {
			text.WriteString(s)
			if text.String() == "第一段" {
				first <- s
			}
			return nil
		})
		if err != nil || text.String() != "第一段第二段" || u.PromptTokens != 9 || u.CompletionTokens != 4 {
			t.Error(text.String(), u, err)
		}
	}()
	// A daily API call already owns the only shared slot. No HTTP request may
	// escape until it releases that slot.
	if calls.Load() != 0 {
		t.Fatal("shared slot ignored")
	}
	<-shared.Sem
	select {
	case <-first:
	case <-time.After(3 * time.Second):
		t.Fatal("text buffered until completion")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("generation did not finish")
	}
	if calls.Load() != 1 || len(shared.Sem) != 0 || shared.OutputTokenLimit != 99 || shared.Allow == nil {
		t.Fatal("shared configuration mutated or slot leaked")
	}
}

func TestAPIInputBoundaryAndIdentity(t *testing.T) {
	var calls atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer s.Close()
	shared := analysis.NewChat(s.URL+"/chat/completions", "synthetic-key", "model", 1)
	c, err := NewAPI(shared, "fixture-no-model")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, msgs := range [][]agent.Message{nil, {{Role: "assistant", Content: "not reviewed"}}, {{Role: "user", ToolCallID: "tool-id"}}, {{Role: "user", Calls: []agent.Call{{Name: "write"}}}}} {
		if _, err := c.Generate(context.Background(), "owner", msgs, func(string) error { return nil }); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := c.Generate(context.Background(), "", []agent.Message{{Role: "user", Content: "x"}}, func(string) error { return nil }); err == nil {
		t.Fatal("unscoped input accepted")
	}
	other := *shared
	other.Model = "different-model"
	c2, err := NewAPI(&other, "fixture-no-model")
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	other.Key = "rotated-key"
	c3, err := NewAPI(&other, "fixture-no-model")
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	if c.Identity() == c2.Identity() || c2.Identity() != c3.Identity() || calls.Load() != 0 || len(c.Identity()) != 64 {
		t.Fatal("identity or no-side-effect boundary failed")
	}
}

func TestAPIEndpointValidation(t *testing.T) {
	for _, endpoint := range []string{"", "http://public.example/chat/completions", "https://user:secret@example.com/chat/completions", "https://example.com/v1", "https://example.com/chat/completions?key=secret", "https://example.com/chat/completions#fragment"} {
		t.Run(endpoint, func(t *testing.T) {
			c, err := NewAPI(analysis.NewChat(endpoint, "synthetic", "model", 1), "")
			if err == nil || c != nil {
				t.Fatal("invalid API configuration accepted")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("credential leaked")
			}
		})
	}
}
