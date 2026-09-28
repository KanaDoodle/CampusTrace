package source

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type queryTransport func(*http.Request) (*http.Response, error)

func (fn queryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestReadOnlySourceQueryRetriesTransientFailureOnce(t *testing.T) {
	for _, failure := range []string{"network", "503"} {
		t.Run(failure, func(t *testing.T) {
			calls, budget := 0, 0
			a := PublicPlatform{Allow: func(context.Context, string, int) (bool, error) { budget++; return true, nil }}
			a.Client = &http.Client{Transport: queryTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				body, _ := io.ReadAll(r.Body)
				if string(body) != `["PositionProjectEnum"]` {
					t.Fatalf("retry lost the query body: %s", body)
				}
				if calls == 1 && failure == "network" {
					return nil, errors.New("temporary connection failure")
				}
				status := 200
				if calls == 1 {
					status = 503
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"success":true}`))}, nil
			})}
			var result map[string]bool
			if err := a.post(context.Background(), d.Source{}, "https://jobs.example.com/query", []string{"PositionProjectEnum"}, &result); err != nil || !result["success"] || calls != 2 || budget != 2 {
				t.Fatalf("query failed: %v result=%v calls=%d budget=%d", err, result, calls, budget)
			}
		})
	}
}

func TestSourceQueryDoesNotRetryAccessDenialOrRateLimits(t *testing.T) {
	for _, status := range []int{403, 429} {
		calls := 0
		a := PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}}
		var result any
		if err := a.get(context.Background(), d.Source{}, "https://jobs.example.com/query", &result); err == nil || calls != 1 {
			t.Fatalf("retried restricted source: status=%d calls=%d err=%v", status, calls, err)
		}
	}
}
