package analysis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

const maxStreamEvent = 64 << 10
const maxStreamWire = 1 << 20
const maxStreamText = 32 << 10

// StreamText is a single, non-retried text-only Chat Completions attempt. A
// successful return requires text, finish_reason=stop and the [DONE] marker.
// Missing usage remains Known=false. Consumer errors close the upstream body.
func (c *ChatClient) StreamText(ctx context.Context, messages any, emit func(string) error) (Usage, error) {
	if emit == nil || c.Sem == nil || c.HTTP == nil {
		return Usage{}, errors.New("model stream consumer required")
	}
	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}
	select {
	case c.Sem <- struct{}{}:
		defer func() { <-c.Sem }()
	case <-ctx.Done():
		return Usage{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return Usage{}, err
	}
	if c.Allow != nil {
		ok, err := c.Allow(ctx)
		if err != nil {
			return Usage{}, err
		}
		if !ok {
			return Usage{}, &HTTPError{429}
		}
	}
	body := c.requestBody(messages, nil, false)
	body["stream"] = true
	body["stream_options"] = map[string]bool{"include_usage": true}
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 256<<10 {
		return Usage{}, &ResponseError{"INVALID_STREAM_INPUT"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(raw))
	if err != nil {
		return Usage{}, &ResponseError{"INVALID_STREAM_ENDPOINT"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	h := *c.HTTP
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := h.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Usage{}, ctx.Err()
		}
		// net/http errors may contain the configured URL. Keep endpoint details,
		// credentials and response content out of task failures and logs.
		return Usage{}, &ResponseError{"STREAM_TRANSPORT"}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Usage{}, &HTTPError{resp.StatusCode}
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "text/event-stream" {
		return Usage{}, &ResponseError{"INVALID_STREAM_TYPE"}
	}
	u, err := readChatStream(resp.Body, func(text string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return emit(text)
	})
	if ctx.Err() != nil {
		return Usage{}, ctx.Err()
	}
	return u, err
}

func readChatStream(input io.Reader, emit func(string) error) (Usage, error) {
	limited := &io.LimitedReader{R: input, N: maxStreamWire + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxStreamEvent+2)
	scanner.Split(sseLine)
	var data strings.Builder
	var usage Usage
	finished, hasText := false, false
	textBytes, events := 0, 0
	for scanner.Scan() {
		line := scanner.Text()
		if limited.N <= 0 {
			return Usage{}, &ResponseError{"STREAM_TOO_LARGE"}
		}
		if line != "" {
			if strings.HasPrefix(line, ":") {
				continue
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			if field == "event" && value == "error" {
				return Usage{}, &ResponseError{"STREAM_PROVIDER_ERROR"}
			}
			if field == "data" {
				if data.Len()+len(value)+1 > maxStreamEvent {
					return Usage{}, &ResponseError{"STREAM_EVENT_TOO_LARGE"}
				}
				data.WriteString(value)
				data.WriteByte('\n')
			}
			continue
		}
		if data.Len() == 0 {
			continue
		}
		payload := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		events++
		if events > 4096 || !utf8.ValidString(payload) {
			return Usage{}, &ResponseError{"INVALID_STREAM_EVENT"}
		}
		if payload == "[DONE]" {
			if !finished || !hasText {
				return Usage{}, &ResponseError{"INCOMPLETE_STREAM"}
			}
			return usage, nil
		}
		var chunk struct {
			Error json.RawMessage `json:"error"`
			Usage *struct {
				Prompt     *int `json:"prompt_tokens"`
				Completion *int `json:"completion_tokens"`
			} `json:"usage"`
			Choices []struct {
				Index  int     `json:"index"`
				Finish *string `json:"finish_reason"`
				Delta  struct {
					Role     string          `json:"role"`
					Content  string          `json:"content"`
					Refusal  string          `json:"refusal"`
					Tools    json.RawMessage `json:"tool_calls"`
					Function json.RawMessage `json:"function_call"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil || (len(chunk.Error) > 0 && string(chunk.Error) != "null") || len(chunk.Choices) > 1 || (len(chunk.Choices) == 0 && chunk.Usage == nil) {
			return Usage{}, &ResponseError{"INVALID_STREAM_EVENT"}
		}
		if chunk.Usage != nil {
			u := chunk.Usage
			if u.Prompt == nil || u.Completion == nil || *u.Prompt < 0 || *u.Completion < 0 || usage.Known {
				return Usage{}, &ResponseError{"INVALID_STREAM_USAGE"}
			}
			usage = Usage{PromptTokens: *u.Prompt, CompletionTokens: *u.Completion, Known: true}
		}
		for _, choice := range chunk.Choices {
			delta := choice.Delta
			tools := strings.TrimSpace(string(delta.Tools))
			function := strings.TrimSpace(string(delta.Function))
			if finished || choice.Index != 0 || (delta.Role != "" && delta.Role != "assistant") ||
				(tools != "" && tools != "null" && tools != "[]") || (function != "" && function != "null") || delta.Refusal != "" {
				return Usage{}, &ResponseError{"NON_TEXT_STREAM"}
			}
			if choice.Finish != nil && *choice.Finish != "" {
				if *choice.Finish != "stop" {
					return Usage{}, &ResponseError{"UNFINISHED_STREAM"}
				}
				finished = true
			}
			if delta.Content != "" {
				textBytes += len(delta.Content)
				if textBytes > maxStreamText {
					return Usage{}, &ResponseError{"STREAM_TEXT_TOO_LARGE"}
				}
				hasText = true
				if err := emit(delta.Content); err != nil {
					return Usage{}, err
				}
			}
		}
	}
	if scanner.Err() != nil {
		return Usage{}, &ResponseError{"STREAM_READ_FAILED"}
	}
	return Usage{}, &ResponseError{"INCOMPLETE_STREAM"}
}

// SSE permits LF, CRLF and CR line endings. Wait for the byte after a trailing
// CR so a network read boundary cannot turn CRLF into two event boundaries.
func sseLine(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		if data[i] == '\r' && i+1 == len(data) && !atEOF {
			return 0, nil, nil
		}
		n := i + 1
		if data[i] == '\r' && n < len(data) && data[n] == '\n' {
			n++
		}
		return n, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
