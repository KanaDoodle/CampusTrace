package source

import (
	"context"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicPlatformFixtures(t *testing.T) {
	cases := []struct{ kind, list, detail string }{
		{"lever", `[{"id":"CaseA","text":"Graduate Backend","hostedUrl":"https://jobs.lever.co/demo/CaseA","categories":{"location":"Shanghai","commitment":"Full-time"}}]`, `{"id":"CaseA","text":"Graduate Backend","hostedUrl":"https://jobs.lever.co/demo/CaseA","descriptionPlain":"2027届 本科及以上 Go","categories":{"location":"Shanghai","commitment":"Full-time"},"applyUrl":"https://jobs.lever.co/demo/CaseA/apply"}`},
		{"greenhouse", `{"jobs":[{"id":123,"title":"Graduate Backend","absolute_url":"https://boards.greenhouse.io/demo/jobs/123","location":{"name":"Shanghai"}}],"meta":{"total":1}}`, `{"id":123,"title":"Graduate Backend","absolute_url":"https://boards.greenhouse.io/demo/jobs/123","location":{"name":"Shanghai"},"content":"<p>2027届 本科及以上 Go</p>"}`},
		{"smartrecruiters", `{"totalFound":1,"content":[{"id":"CaseA","name":"Graduate Backend","location":{"city":"Shanghai","country":"cn"}}]}`, `{"id":"CaseA","name":"Graduate Backend","location":{"city":"Shanghai","country":"cn"},"jobAd":{"sections":{"jobDescription":{"text":"<p>2027届 本科及以上 Go</p>"}}},"applyUrl":"https://jobs.smartrecruiters.com/demo/CaseA"}`},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				if calls == 1 {
					fmt.Fprint(w, c.list)
				} else {
					fmt.Fprint(w, c.detail)
				}
			}))
			defer server.Close()
			client := server.Client()
			client.Transport = rewriteTransport{base: server.URL}
			a := PublicPlatform{Client: client}
			src := d.Source{ID: "source", Name: "Demo", Adapter: c.kind, Tenant: "demo"}
			refs, err := a.Discover(context.Background(), src, d.WatchTarget{})
			if err != nil || len(refs) != 1 {
				t.Fatalf("%+v %v", refs, err)
			}
			snap, err := a.FetchPosting(context.Background(), src, refs[0])
			if err != nil || !strings.Contains(snap.Text, "2027届") || snap.Status != "SUCCESS" {
				t.Fatalf("%+v %v", snap, err)
			}
			if strings.Contains(snap.Text, "apply: PRESENT") || strings.Contains(snap.Text, "status: OPEN") {
				t.Fatal("adapter manufactured a business claim")
			}
		})
	}
}

type rewriteTransport struct{ base string }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	clone.URL = &u
	parts := strings.SplitN(r.base, "://", 2)
	clone.URL.Scheme = parts[0]
	clone.URL.Host = parts[1]
	return http.DefaultTransport.RoundTrip(clone)
}
func TestDiscoveryFailurePolicy(t *testing.T) {
	for _, code := range []int{403, 429, 500, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			_, err := a.Discover(context.Background(), d.Source{Adapter: "lever", Tenant: "demo"}, d.WatchTarget{})
			var e *FetchError
			if !AsFetchError(err, &e) || e.Retryable != (code == 429 || code >= 500) {
				t.Fatalf("%d %v", code, err)
			}
		})
	}
	a := PublicPlatform{}
	if _, err := a.Discover(context.Background(), d.Source{Adapter: "unsupported", Tenant: "demo"}, d.WatchTarget{}); err == nil {
		t.Fatal("unsupported succeeded")
	}
}

func TestPublicPlatformTimeoutBoundsAndIdentity(t *testing.T) {
	for _, tenant := range []string{"../private", "x/y", "user@host", "127.0.0.1:80", ""} {
		if _, err := PlatformURL(d.Source{Adapter: "lever", Tenant: tenant}); err == nil {
			t.Fatal("untrusted endpoint accepted", tenant)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Timeout: 10 * time.Millisecond, Transport: rewriteTransport{srv.URL}}}
	_, err := a.Discover(context.Background(), d.Source{Adapter: "lever", Tenant: "fixture"}, d.WatchTarget{})
	var e *FetchError
	if !errors.As(err, &e) || !e.Retryable {
		t.Fatal("timeout not transient", err)
	}
}
