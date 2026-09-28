package analysis

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestModelResponseFailuresKeepSafeTypedReasons(t *testing.T) {
	for _, tc := range []struct{ body, reason string }{
		{"gateway returned a non-JSON body with private content", "INVALID_JSON"},
		{`{"choices":[]}`, "INVALID_CHOICES"},
		{strings.Repeat(" ", 1<<20) + `{"choices":[]}`, "TOO_LARGE"},
	} {
		client := NewChat("https://api.deepseek.com/chat/completions", "synthetic-secret", "deepseek-flash", 1)
		client.HTTP = &http.Client{Transport: modelTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}
		_, err := client.CompleteJSON(context.Background(), nil, nil)
		var response *ResponseError
		if !errors.As(err, &response) || response.Reason != tc.reason || strings.Contains(err.Error(), "private content") {
			t.Fatal(err)
		}
	}
}
