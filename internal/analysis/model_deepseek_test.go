package analysis

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type modelTransport func(*http.Request) (*http.Response, error)

func (fn modelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestDeepSeekStructuredRequestUsesFastJSONMode(t *testing.T) {
	client := NewChat("https://api.deepseek.com/chat/completions", "test-key", "deepseek-flash", 1)
	client.HTTP = &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.deepseek.com" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("incorrect provider request")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "deepseek-flash" || body["thinking"].(map[string]any)["type"] != "disabled" || body["response_format"].(map[string]any)["type"] != "json_object" {
			t.Fatalf("missing DeepSeek JSON settings: %+v", body)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"role":"assistant","content":"{\"suggestions\":[],\"projects\":[]}"}}]}`)), Header: make(http.Header)}, nil
	})}
	message, err := client.CompleteJSON(context.Background(), []map[string]string{{"role": "user", "content": "测试"}}, nil)
	if err != nil || !strings.Contains(string(message), "suggestions") {
		t.Fatalf("completion %s: %v", message, err)
	}
}

func TestGenericProviderKeepsExistingRequestShape(t *testing.T) {
	client := NewChat("https://api.example.com/v1/chat/completions", "test-key", "other", 1)
	client.HTTP = &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if _, ok := body["thinking"]; ok {
			t.Fatal("provider-specific thinking setting leaked")
		}
		if _, ok := body["response_format"]; ok {
			t.Fatal("unsupported JSON mode leaked")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{}"}}]}`)), Header: make(http.Header)}, nil
	})}
	if _, err := client.CompleteJSON(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
}
