package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const textEvent = `{"model":"canonical-model-revision","choices":[{"index":0,"delta":{"content":"文字"},"finish_reason":null}]}`
const stopEvent = `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
const usageEvent = `{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3}}`

func streamData(payloads ...string) string {
	return "data: " + strings.Join(payloads, "\n\ndata: ") + "\n\n"
}

type oneByteReader struct{ io.Reader }

func (r oneByteReader) Read(p []byte) (int, error) { return r.Reader.Read(p[:1]) }

func TestChatStreamFramingAndUsage(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		t.Run(fmt.Sprintf("ending-%q", newline), func(t *testing.T) {
			input := ": keepalive\n\nevent: message\ndata: {\ndata: \"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" + streamData(textEvent, stopEvent, usageEvent, "[DONE]")
			var output strings.Builder
			u, err := readChatStream(oneByteReader{strings.NewReader(strings.ReplaceAll(input, "\n", newline))}, func(s string) error { output.WriteString(s); return nil })
			if err != nil || output.String() != "文字" || !u.Known || u.PromptTokens != 12 || u.CompletionTokens != 3 {
				t.Fatal(output.String(), u, err)
			}
		})
	}
	var output strings.Builder
	u, err := readChatStream(strings.NewReader(streamData(textEvent, stopEvent, "[DONE]")), func(s string) error { output.WriteString(s); return nil })
	if err != nil || u.Known || output.String() != "文字" {
		t.Fatal("optional usage fabricated or required", u, err)
	}
}

func TestChatStreamRejectsUnsafeIncompleteAndUnboundedData(t *testing.T) {
	cases := map[string]string{
		"no-done":         streamData(textEvent, stopEvent),
		"no-finish":       streamData(textEvent, "[DONE]"),
		"no-text":         streamData(stopEvent, "[DONE]"),
		"unframed-done":   streamData(textEvent, stopEvent) + "data: [DONE]",
		"malformed":       streamData(`{"choices":`),
		"error":           streamData(`{"error":{"message":"private-provider-error"}}`),
		"error-event":     "event: error\ndata: private-provider-error\n\n",
		"invalid-object":  streamData(`{}`),
		"tool":            streamData(`{"choices":[{"delta":{"tool_calls":[{"function":{"name":"write"}}]}}]}`),
		"function":        streamData(`{"choices":[{"delta":{"function_call":{"name":"write"}}}]}`),
		"refusal":         streamData(`{"choices":[{"delta":{"refusal":"refused"}}]}`),
		"tool-role":       streamData(`{"choices":[{"delta":{"role":"tool","content":"bad"}}]}`),
		"two-choices":     streamData(`{"choices":[{"index":0,"delta":{}},{"index":1,"delta":{}}]}`),
		"wrong-index":     streamData(`{"choices":[{"index":1,"delta":{"content":"bad"}}]}`),
		"length":          streamData(textEvent, strings.Replace(stopEvent, `"stop"`, `"length"`, 1), "[DONE]"),
		"filter":          streamData(textEvent, strings.Replace(stopEvent, `"stop"`, `"content_filter"`, 1), "[DONE]"),
		"late-text":       streamData(textEvent, stopEvent, textEvent, "[DONE]"),
		"reasoning-only":  streamData(`{"choices":[{"delta":{"reasoning_content":"private-reasoning"}}]}`, stopEvent, "[DONE]"),
		"bad-usage":       streamData(textEvent, stopEvent, strings.Replace(usageEvent, `:12`, `:-1`, 1), "[DONE]"),
		"partial-usage":   streamData(textEvent, stopEvent, `{"choices":[],"usage":{"prompt_tokens":12}}`, "[DONE]"),
		"duplicate-usage": streamData(textEvent, stopEvent, usageEvent, usageEvent, "[DONE]"),
		"event-limit":     "data: " + strings.Repeat("x", maxStreamEvent+1) + "\n\n",
		"multiline-limit": strings.Repeat("data: x\n", maxStreamEvent) + "\n",
		"wire-limit":      strings.Repeat(": heartbeat\n", maxStreamWire/12+1),
		"text-limit":      streamData(strings.Replace(textEvent, "文字", strings.Repeat("x", maxStreamText+1), 1), stopEvent, "[DONE]"),
		"event-count":     strings.Repeat(streamData(`{"choices":[{"delta":{}}]}`), 4097),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			var output strings.Builder
			_, err := readChatStream(strings.NewReader(input), func(s string) error { output.WriteString(s); return nil })
			if err == nil || strings.Contains(err.Error(), "private-") || strings.Contains(output.String(), "private-") || output.Len() > maxStreamText {
				t.Fatal("unsafe stream accepted or leaked", output.Len(), err)
			}
		})
	}
	stop := errors.New("consumer stopped")
	if _, err := readChatStream(strings.NewReader(streamData(textEvent, stopEvent, "[DONE]")), func(string) error { return stop }); !errors.Is(err, stop) {
		t.Fatal("consumer failure not propagated", err)
	}
}

func TestStreamTextCancelClosesBodyAndReleasesSlot(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, streamData(textEvent))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer s.Close()
	c := NewChat(s.URL, "synthetic-key", "model", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := c.StreamText(ctx, nil, func(string) error { cancel(); return nil }); done <- err }()
	for _, ch := range []<-chan struct{}{started, stopped} {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
			t.Fatal("cancellation did not reach HTTP provider")
		}
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reader leaked")
	}
	if len(c.Sem) != 0 {
		t.Fatal("concurrency slot leaked")
	}
}

func TestStreamTextErrorsNeverRetryOrFollowRedirect(t *testing.T) {
	var redirected atomic.Int64
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	for _, status := range []int{http.StatusTemporaryRedirect, 401, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(status)
				fmt.Fprint(w, "private-error synthetic-key")
			}))
			defer s.Close()
			c := NewChat(s.URL, "synthetic-key", "model", 1)
			_, err := c.StreamText(context.Background(), nil, func(string) error { return nil })
			var h *HTTPError
			if !errors.As(err, &h) || h.Status != status || calls.Load() != 1 || redirected.Load() != 0 || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "synthetic-key") {
				t.Fatal("HTTP error retried, redirected or leaked", err, calls.Load(), redirected.Load())
			}
		})
	}
}

func TestStreamTextRetainsProviderSettings(t *testing.T) {
	for _, tc := range []struct{ endpoint, model, limit string }{
		{"https://api.deepseek.com/chat/completions", "deepseek-flash", "max_tokens"},
		{"https://api.openai.com/v1/chat/completions", "gpt-6-luna", "max_completion_tokens"},
		{"https://api.example.invalid/v1/chat/completions", "other", "max_tokens"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			c := NewChat(tc.endpoint, "synthetic-key", tc.model, 1)
			c.OutputTokenLimit = 512
			c.HTTP = &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body[tc.limit] != float64(512) || body["stream"] != true {
					t.Fatal(body)
				}
				if tc.model == "deepseek-flash" && body["thinking"].(map[string]any)["type"] != "disabled" {
					t.Fatal(body)
				}
				if tc.model == "gpt-6-luna" && (body["reasoning_effort"] != "none" || body["temperature"] != nil) {
					t.Fatal(body)
				}
				if _, ok := body["response_format"]; ok {
					t.Fatal("narration unexpectedly requested JSON mode")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(streamData(textEvent, stopEvent, "[DONE]")))}, nil
			})}
			_, err := c.StreamText(context.Background(), []map[string]string{{"role": "user", "content": "reviewed"}}, func(string) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStreamTextRejectsNonSSE(t *testing.T) {
	for _, contentType := range []string{"application/json", "text/plain", ""} {
		t.Run(contentType, func(t *testing.T) {
			c := NewChat("https://example.invalid/chat/completions", "", "model", 1)
			c.HTTP = &http.Client{Transport: modelTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(streamData(textEvent, stopEvent, "[DONE]")))}, nil
			})}
			if _, err := c.StreamText(context.Background(), nil, func(string) error { t.Error("non SSE response emitted"); return nil }); err == nil {
				t.Fatal("invalid content type accepted")
			}
		})
	}
}

func TestStreamTextTimeoutWhileWaitingForBodyOrSharedSlot(t *testing.T) {
	var calls atomic.Int64
	stopped := make(chan struct{}, 1)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		stopped <- struct{}{}
	}))
	defer s.Close()
	c := NewChat(s.URL, "", "model", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.StreamText(ctx, nil, func(string) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) || len(c.Sem) != 0 {
		t.Fatal(err, len(c.Sem))
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout did not close body")
	}
	c.Sem <- struct{}{}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel2()
	_, err = c.StreamText(ctx2, nil, func(string) error { return nil })
	<-c.Sem
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal("canceled slot waiter called provider", err, calls.Load())
	}
}

func FuzzChatStream(f *testing.F) {
	for _, seed := range []string{streamData(textEvent, stopEvent, usageEvent, "[DONE]"), "data: {\r\ndata: }\r\n\r\n", ": ping\n\n", "data: [DONE]\n\n"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > maxStreamWire+16 {
			return
		}
		var text bytes.Buffer
		_, _ = readChatStream(bytes.NewReader(raw), func(s string) error { text.WriteString(s); return nil })
		if text.Len() > maxStreamText {
			t.Fatal("unbounded stream output")
		}
	})
}
