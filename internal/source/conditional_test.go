package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConditionalGETUsesExactCachedBodyAndRevalidates(t *testing.T) {
	calls := 0
	var entry HTTPEntry
	var key string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"v1"`)
			w.Write([]byte(`{"value":42}`))
			return
		}
		if r.Header.Get("If-None-Match") != `"v1"` {
			t.Error("missing validator")
		}
		w.WriteHeader(304)
	}))
	defer server.Close()
	a := PublicPlatform{Client: server.Client(), CacheRead: func(_ context.Context, id, k string) (HTTPEntry, error) {
		if id != "s" {
			t.Fatal(id)
		}
		if key != "" && key != k {
			t.Fatal("key drift")
		}
		return entry, nil
	}, CacheWrite: func(_ context.Context, id, k string, v HTTPEntry) error { key = k; entry = v; return nil }}
	for i := 0; i < 2; i++ {
		var v struct{ Value int }
		if e := a.get(context.Background(), d.Source{ID: "s"}, server.URL, &v); e != nil || v.Value != 42 {
			t.Fatal(v, e)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestConditionalRejectsUnbound304AndNeverConditionsPOST(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Error("conditioned POST")
		}
		w.WriteHeader(304)
	}))
	defer server.Close()
	a := PublicPlatform{Client: server.Client()}
	var v any
	if a.get(context.Background(), d.Source{}, server.URL, &v) == nil {
		t.Fatal("accepted unbound 304")
	}
	a.CacheRead = func(context.Context, string, string) (HTTPEntry, error) {
		t.Fatal("POST read GET cache")
		return HTTPEntry{}, nil
	}
	if a.post(context.Background(), d.Source{}, server.URL, map[string]string{}, &v) == nil {
		t.Fatal("accepted POST 304")
	}
}
