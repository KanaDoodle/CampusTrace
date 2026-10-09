package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/agent"
	"github.com/KanaDoodle/CampusTrace/internal/auth"
	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/KanaDoodle/CampusTrace/internal/rag"
	"github.com/KanaDoodle/CampusTrace/internal/transport"
)

func TestKnowledgeHTTPPreservesLargeEscapedTextAndRequiresIndexConfirmation(t *testing.T) {
	ctx, store, queue, owner, _ := setup(t)
	authn := auth.Service{Store: store, Secret: []byte("synthetic-retrieval-http-secret-more-than-32-bytes")}
	token, e := authn.Token(owner)
	must(t, e)
	service := &rag.Service{Store: store}
	handler := (&transport.API{Store: store, Queue: queue, Auth: authn, Tools: &agent.Tools{Store: store, RAG: service}}).Handler()
	text := strings.Repeat("\"\\\n ", 12000)
	response := matchingRequest(handler, token, "/api/documents", "POST", rag.Document{Title: "Escaped text", Text: text})
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var doc rag.Document
	must(t, json.Unmarshal(response.Body.Bytes(), &doc))
	if doc.Text != text {
		t.Fatal("valid text was truncated")
	}
	response = matchingRequest(handler, token, "/api/documents?offset=0", "GET", nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), "\\\"\\\\") {
		t.Fatal("list did not return bounded summaries", response.Body.String())
	}
	response = matchingRequest(handler, token, "/api/knowledge/index", "POST", map[string]any{"key": strings.Repeat("a", 64)})
	if response.Code != 400 {
		t.Fatal("index without explicit confirmation", response.Code)
	}
	var n int
	must(t, store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_vectors WHERE user_id=?", owner).Scan(&n))
	if n != 0 {
		t.Fatal("unconfirmed request wrote vectors")
	}
}

type retrievalTransport func(*http.Request) (*http.Response, error)

func (f retrievalTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestKnowledgeIndexReuseOwnershipPrivacyAndDeletion(t *testing.T) {
	ctx, store, _, owner, _ := setup(t)
	other, e := store.NewUser(ctx, "other-"+owner+"@synthetic.test", "unused")
	must(t, e)
	t.Cleanup(func() { store.DB.ExecContext(context.Background(), "DELETE FROM users WHERE id=?", other) })
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var v struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&v)
		data := []any{}
		for i, text := range v.Input {
			if strings.Contains(text, "张三") || strings.Contains(text, "13800138000") || strings.Contains(text, "personal@example.com") {
				t.Error("PII sent to provider", text)
			}
			data = append(data, map[string]any{"index": i, "embedding": []float64{1, 0}})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	client := server.Client()
	base := client.Transport
	client.Transport = retrievalTransport(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return base.RoundTrip(r)
	})
	cfg := modelconfig.Config{URL: "https://retrieval.example.com/v1/embeddings", Model: "synthetic", APIKey: "never-persist-secret"}
	service := rag.Service{Store: store, Sem: make(chan struct{}, 2), Options: rag.Options{Embedding: &cfg, MaskName: "张三"}, Provider: rag.Provider{HTTP: client}, Cache: &rag.QueryCache{}}
	doc, e := service.Ingest(ctx, owner, rag.Document{Title: "Redis 张三", Text: "消息 pending 13800138000 personal@example.com"})
	must(t, e)
	preview, e := service.PreviewIndex(ctx, owner)
	must(t, e)
	if preview.Pending != 1 || len(preview.Inputs) != 1 {
		t.Fatal("preview", preview)
	}
	otherPreview, e := service.PreviewIndex(ctx, other)
	must(t, e)
	if otherPreview.Total != 0 || otherPreview.Key == preview.Key {
		t.Fatal("cross-account preview", otherPreview)
	}
	_, e = service.Index(ctx, owner, strings.Repeat("a", 64))
	if !errors.Is(e, p.ErrStaleInput) || calls.Load() != 0 {
		t.Fatal("stale preview sent", e, calls.Load())
	}
	result, e := service.Index(ctx, owner, preview.Key)
	must(t, e)
	if result.Indexed != 1 || calls.Load() != 1 {
		t.Fatal("index", result, calls.Load())
	}
	ready, e := service.PreviewIndex(ctx, owner)
	must(t, e)
	if ready.Indexed != 1 || ready.Pending != 0 {
		t.Fatal(ready)
	}
	_, e = service.Index(ctx, owner, ready.Key)
	must(t, e)
	if calls.Load() != 1 {
		t.Fatal("already indexed called provider")
	}
	out, e := service.SearchDetailed(ctx, owner, "接收方挂了怎么办", 5)
	must(t, e)
	if out.Retrieval.Mode != "hybrid" || len(out.Hits) != 1 || out.Hits[0].DocumentID != doc.ID || out.Hits[0].Vector != nil {
		t.Fatal(out)
	}
	beforeCalls := calls.Load()
	second, e := service.SearchDetailed(ctx, owner, "接收方挂了怎么办", 5)
	must(t, e)
	if !second.Retrieval.QueryCacheHit || calls.Load() != beforeCalls {
		t.Fatal("repeat query paid twice", second)
	}
	out, e = service.SearchDetailed(ctx, other, "接收方挂了怎么办", 5)
	must(t, e)
	if len(out.Hits) != 0 {
		t.Fatal("cross-account retrieval", out)
	}
	var raw string
	must(t, store.DB.QueryRowContext(ctx, "SELECT CAST(body AS CHAR) FROM knowledge_vectors WHERE user_id=?", owner).Scan(&raw))
	if strings.Contains(raw, cfg.APIKey) || strings.Contains(raw, "13800138000") {
		t.Fatal("secret persisted", raw)
	}
	if e = service.DeleteDocument(ctx, other, doc.ID); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("cross-account delete", e)
	}
	must(t, service.DeleteDocument(ctx, owner, doc.ID))
	var count int
	must(t, store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_vectors WHERE user_id=?", owner).Scan(&count))
	if count != 0 {
		t.Fatal("orphan vectors", count)
	}
}

func TestKnowledgeSearchFencesDeletedInFlightMaterials(t *testing.T) {
	ctx, store, _, owner, _ := setup(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			close(entered)
			<-release
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1, 0}}}})
	}))
	defer server.Close()
	client := server.Client()
	base := client.Transport
	client.Transport = retrievalTransport(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return base.RoundTrip(r)
	})
	cfg := modelconfig.Config{URL: "https://retrieval.example.com/v1/embeddings", Model: "synthetic", APIKey: "not-persisted"}
	service := rag.Service{Store: store, Options: rag.Options{Embedding: &cfg}, Provider: rag.Provider{HTTP: client}}
	doc, e := service.Ingest(ctx, owner, rag.Document{Title: "Test", Text: "Redis pending"})
	must(t, e)
	preview, e := service.PreviewIndex(ctx, owner)
	must(t, e)
	_, e = service.Index(ctx, owner, preview.Key)
	must(t, e)
	done := make(chan error, 1)
	go func() { _, e := service.SearchDetailed(ctx, owner, "换种说法", 5); done <- e }()
	<-entered
	must(t, service.DeleteDocument(ctx, owner, doc.ID))
	close(release)
	if e = <-done; !errors.Is(e, p.ErrStaleInput) {
		t.Fatal("deleted material escaped search fence", e)
	}
}
func TestKnowledgeConcurrentIndexLeaseAndClearFencesInFlight(t *testing.T) {
	ctx, store, _, owner, _ := setup(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1, 0}}}})
	}))
	defer server.Close()
	client := server.Client()
	base := client.Transport
	client.Transport = retrievalTransport(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return base.RoundTrip(r)
	})
	cfg := modelconfig.Config{URL: "https://retrieval.example.com/v1/embeddings", Model: "synthetic", APIKey: "not-persisted"}
	service := rag.Service{Store: store, Sem: make(chan struct{}, 2), Options: rag.Options{Embedding: &cfg}, Provider: rag.Provider{HTTP: client}}
	_, e := service.Ingest(ctx, owner, rag.Document{Title: "Test", Text: "Redis pending"})
	must(t, e)
	preview, e := service.PreviewIndex(ctx, owner)
	must(t, e)
	done := make(chan error, 1)
	go func() { _, e := service.Index(ctx, owner, preview.Key); done <- e }()
	<-entered
	_, e = service.Index(ctx, owner, preview.Key)
	if !errors.Is(e, p.ErrConflict) {
		t.Fatal("duplicate paid batch allowed", e)
	}
	must(t, service.ClearVectors(ctx, owner))
	close(release)
	e = <-done
	if !errors.Is(e, p.ErrStaleInput) {
		t.Fatal("cleared index resurrected", e)
	}
	var count int
	must(t, store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_vectors WHERE user_id=?", owner).Scan(&count))
	if count != 0 {
		t.Fatal("stale vectors committed")
	}
}
