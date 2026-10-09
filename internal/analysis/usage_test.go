package analysis

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUsageRequiresBothNonNegativeFields(t *testing.T) {
	for _, v := range []struct {
		raw   string
		known bool
	}{{`{"prompt_tokens":20,"completion_tokens":3}`, true}, {`{}`, false}, {`{"prompt_tokens":20}`, false}, {`null`, false}, {`{"prompt_tokens":-1,"completion_tokens":3}`, false}} {
		t.Run(v.raw, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":` + v.raw + `}`))
			}))
			defer s.Close()
			c := NewChat(s.URL, "test-key", "test", 1)
			_, u, e := c.CompleteWithUsage(context.Background(), []any{}, nil)
			if e != nil || u.Known != v.known {
				t.Fatal(u, e)
			}
		})
	}
}
