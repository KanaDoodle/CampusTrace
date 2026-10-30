package searchservice

import (
	"context"
	"errors"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const queryToken = "test-search-query-token-0123456789012345"
const writeToken = "test-search-write-token-0123456789012345"
const dataset = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const alice = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const bob = "cccccccccccccccccccccccccccccccc"

func document(id, title, text, visibility, owner string) a.Document {
	d := a.Document{Dataset: dataset, ID: id, Version: 1, Company: "测试公司", Title: title, Text: text, Visibility: visibility, Owner: owner, JobType: "FULL_TIME", Locations: []string{"上海"}}
	d.Hash = d.ContentHash()
	return d
}

type unavailable struct{}

func (unavailable) Status(context.Context) (a.Status, error) { return a.Status{}, a.ErrUnavailable }
func (unavailable) Apply(context.Context, a.Batch) error     { return a.ErrUnavailable }
func (unavailable) Query(context.Context, a.Query) (a.Result, error) {
	return a.Result{}, a.ErrUnavailable
}
func (unavailable) Rebuild(context.Context) (a.Status, error) { return a.Status{}, a.ErrUnavailable }
func TestHTTPBoundary(t *testing.T) {
	server, e := NewServer(unavailable{}, queryToken, writeToken)
	if e != nil {
		t.Fatal(e)
	}
	h := server.Handler()
	for _, c := range []struct {
		method, path, token, body string
		status                    int
	}{{"GET", "/v1/status", "", "", 401}, {"POST", "/v1/batch", queryToken, `{}`, 401}, {"POST", "/v1/query", writeToken, `{}`, 401}, {"POST", "/v1/query", queryToken, `{"user":"x","unexpected":true}`, 400}, {"POST", "/v1/query", queryToken, `{} {}`, 400}, {"GET", "/v1/status", queryToken, "", 503}, {"GET", "/healthz", "", "", 200}} {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
		r.Header.Set("Authorization", "Bearer "+c.token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Errorf("%s %s got %d want %d", c.method, c.path, w.Code, c.status)
		}
		if strings.Contains(w.Body.String(), "token") {
			t.Fatal("credential leaked")
		}
	}
}
func TestOpenSearchContract(t *testing.T) {
	endpoint := os.Getenv("OPENSEARCH_TEST_URL")
	if endpoint == "" {
		t.Skip("requires isolated OpenSearch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	alias := "test-contract-" + time.Now().UTC().Format("150405000000000")
	backend, e := NewBackend(endpoint, alias)
	if e != nil {
		t.Fatal(e)
	}
	defer backend.request(context.Background(), "DELETE", "/"+alias+"-*", nil)
	srv, _ := NewServer(backend, queryToken, writeToken)
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()
	client, e := a.NewClient(httpServer.URL, queryToken, writeToken)
	if e != nil {
		t.Fatal(e)
	}
	status, e := client.Status(ctx)
	if e != nil {
		t.Fatal("status", e)
	}
	docs := []a.Document{document("11111111111111111111111111111111", "服务器工程师", "C++17 Redis 分布式网络开发", "GLOBAL", ""), document("22222222222222222222222222222222", "开发工程师", "Golang MySQL", "PRIVATE", alice), document("33333333333333333333333333333333", "客户端工程师", "C# Unity", "PRIVATE", bob)}
	apply := func(d ...a.Document) error {
		return client.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: status.Generation, Documents: d})
	}
	if e = apply(docs...); e != nil {
		t.Fatal("apply", e)
	}
	if e = apply(docs...); e != nil {
		t.Fatal("idempotent replay", e)
	}
	query := func(user, text string) (a.Result, error) {
		return client.Query(ctx, a.Query{Dataset: dataset, Generation: status.Generation, User: user, Text: text, Mode: "ALL", Limit: 100})
	}
	for _, c := range []struct {
		user, text string
		count      int
	}{{alice, "Go", 1}, {bob, "Go", 0}, {alice, "C++", 1}, {bob, "C++", 1}, {alice, "C#", 0}, {bob, "C#", 1}, {alice, "分布式", 1}, {alice, "", 2}} {
		r, e := query(c.user, c.text)
		if e != nil || len(r.Hits) != c.count {
			t.Fatalf("query %q %s: %+v %v", c.text, c.user, r, e)
		}
	}
	changed := docs[0]
	changed.Version = 2
	changed.Text = "Python"
	changed.Hash = changed.ContentHash()
	if e = apply(changed); e != nil {
		t.Fatal(e)
	}
	if e = apply(docs[0]); e != nil {
		t.Fatal("older event must be harmless", e)
	}
	conflict := changed
	conflict.Text = "C++"
	conflict.Hash = conflict.ContentHash()
	if e = apply(conflict); !errors.Is(e, a.ErrConflict) {
		t.Fatal("equal version conflict accepted", e)
	}
	tombstone := a.Document{Dataset: dataset, ID: changed.ID, Version: 3, Deleted: true}
	tombstone.Hash = tombstone.ContentHash()
	if e = apply(tombstone); e != nil {
		t.Fatal(e)
	}
	if e = apply(changed); e != nil {
		t.Fatal(e)
	}
	r, e := query(alice, "Python")
	if e != nil || len(r.Hits) != 0 {
		t.Fatal("deleted job resurrected", r, e)
	}
	old := status
	status, e = client.Rebuild(ctx)
	if e != nil || status.Generation == old.Generation {
		t.Fatal("rebuild", status, e)
	}
	if e = client.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: old.Generation, Documents: docs}); !errors.Is(e, a.ErrConflict) {
		t.Fatal("old generation accepted", e)
	}
	r, e = query(alice, "")
	if e != nil || len(r.Hits) != 0 {
		t.Fatal("rebuild not empty", r, e)
	}
	if e = apply(docs...); e != nil {
		t.Fatal("replay", e)
	}
	// Losing the concrete index must change generation even with the same alias.
	before := status.Generation
	code, _, e := backend.request(ctx, http.MethodDelete, "/"+strings.Split(status.Generation, ":")[0], nil)
	if e != nil || code != 200 {
		t.Fatal("wipe", code, e)
	}
	status, e = client.Status(ctx)
	if e != nil || status.Generation == before {
		t.Fatal("lost index generation not detected", status, e)
	}
}
