package analysis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMatchingRequestsHaveJSONModeAndOutputBudget(t *testing.T) {
	for _, provider := range []struct{ url, model, limitField string }{{"https://api.openai.com/v1/chat/completions", "gpt-6-luna", "max_completion_tokens"}, {"https://api.deepseek.com/chat/completions", "deepseek-flash", "max_tokens"}} {
		t.Run(provider.model, func(t *testing.T) {
			client := NewChat(provider.url, "synthetic-test-key", provider.model, 1)
			client.OutputTokenLimit = 8000
			client.HTTP = &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body[provider.limitField] != float64(8000) || body["response_format"].(map[string]any)["type"] != "json_object" {
					t.Fatalf("matching cost/output bound missing: %+v", body)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{}"}}]}`)), Header: make(http.Header)}, nil
			})}
			if _, err := client.CompleteJSON(context.Background(), []map[string]string{{"role": "user", "content": "JSON"}}, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}
