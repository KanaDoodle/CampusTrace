package rag

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KanaDoodle/CampusTrace/internal/modelconfig"
)

func testProvider(t *testing.T, handle http.HandlerFunc) (Provider, modelconfig.Config) {
	t.Helper()
	server := httptest.NewTLSServer(handle)
	t.Cleanup(server.Close)
	client := server.Client()
	base := client.Transport
	client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Host = strings.TrimPrefix(server.URL, "https://")
		return base.RoundTrip(r)
	})
	return Provider{HTTP: client}, modelconfig.Config{URL: "https://retrieval.example.com/v1/embeddings", Model: "synthetic-embedding", APIKey: "never-log-this-secret"}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderValidatesVectorIndexDimensionAndErrorPrivacy(t *testing.T) {
	responses := []string{
		`{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}],"usage":{"prompt_tokens":12}}`,
		`{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`,
		`{"data":[{"embedding":[1,0]},{"index":1,"embedding":[0,1]}]}`,
		`{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[0,1,2]}]}`,
		`{"data":[{"index":0,"embedding":[0,0]},{"index":1,"embedding":[0,1]}]}`,
	}
	for i, response := range responses {
		p, cfg := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer never-log-this-secret" {
				t.Error("missing authentication")
			}
			w.Write([]byte(response))
		})
		vs, usage, e := p.Embed(context.Background(), cfg, []string{"甲", "乙"})
		if i == 0 {
			if e != nil || vs[0][0] != 1 || usage.InputTokens == nil || *usage.InputTokens != 12 {
				t.Fatal("out-of-order vector mapping", vs, usage, e)
			}
		} else if e == nil {
			t.Fatal("invalid provider output accepted", i)
		}
	}
	p, cfg := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte("never-log-this-secret personal@example.com"))
	})
	_, _, e := p.Embed(context.Background(), cfg, []string{"text"})
	if e == nil || e.Error() != "RETRIEVAL_AUTH_FAILED" {
		t.Fatal("unsafe provider error", e)
	}
	if validVector([]float64{math.NaN()}) || validVector([]float64{math.Inf(1)}) {
		t.Fatal("nonfinite vector")
	}
	cfg.URL = "http://127.0.0.1/embeddings"
	if _, _, e = p.Embed(context.Background(), cfg, []string{"text"}); !errors.Is(e, modelconfig.ErrInvalid) {
		t.Fatal("internal endpoint accepted", e)
	}
}

func TestHybridRecallsParaphraseCachesQueryAndDoesNotMixModels(t *testing.T) {
	calls := 0
	p, cfg := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float64{1, 0}}}})
	})
	chunks := []Chunk{{ID: "a", DocumentID: "a", Title: "消息 PEL", Text: "Redis Streams XAUTOCLAIM"}, {ID: "b", DocumentID: "b", Title: "HTML", Text: "页面 CSS"}}
	vectors := map[string]SemanticVector{"a": NewSemanticVector(chunks[0], []float64{1, 0}, ""), "b": NewSemanticVector(chunks[1], []float64{0, 1}, "")}
	s := Service{Options: Options{Embedding: &cfg}, Provider: p, Cache: &QueryCache{}}
	for i := 0; i < 2; i++ {
		result, e := s.Rank(context.Background(), "alice", "接收方挂了怎么办", chunks, vectors, 5)
		if e != nil || len(result.Hits) != 1 || result.Hits[0].ID != "a" || result.Retrieval.Mode != "hybrid" {
			t.Fatal("paraphrase recall", result, e)
		}
		if i == 1 && !result.Retrieval.QueryCacheHit {
			t.Fatal("query cache missing")
		}
	}
	if calls != 1 {
		t.Fatal("duplicate query paid twice", calls)
	}
	s.Rank(context.Background(), "bob", "接收方挂了怎么办", chunks, vectors, 5)
	if calls != 2 {
		t.Fatal("cache account leakage")
	}
	cfg.Model = "new-model"
	s.Options.Embedding = &cfg
	s.Rank(context.Background(), "alice", "接收方挂了怎么办", chunks, vectors, 5)
	if calls != 3 {
		t.Fatal("cache model leakage")
	}
	vectors["a"] = NewSemanticVector(chunks[0], []float64{1, 0}, "")
	chunks[0].Text = "changed after index"
	result, e := s.Rank(context.Background(), "alice", "接收方挂了怎么办", chunks, vectors, 5)
	if e != nil || len(result.Hits) != 0 {
		t.Fatal("stale vector reused", result, e)
	}
	if len(KeywordRank("接收方挂了怎么办", chunks)) != 0 {
		t.Fatal("test query unexpectedly overlaps keywords")
	}
}

func TestFallbackRerankBoundAndSanitizedPayload(t *testing.T) {
	documents := 0
	p, cfg := testProvider(t, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Query     string
			Documents []string
		}
		json.NewDecoder(r.Body).Decode(&in)
		documents = len(in.Documents)
		raw, _ := json.Marshal(in)
		if strings.Contains(string(raw), "张三") || strings.Contains(string(raw), "13800138000") {
			t.Fatal("PII sent", string(raw))
		}
		results := []any{}
		for i := range in.Documents {
			results = append(results, map[string]any{"index": i, "relevance_score": float64(i) / 20})
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	cfg.URL = "https://retrieval.example.com/v1/rerank"
	chunks := []Chunk{}
	for i := 0; i < 40; i++ {
		id := string(rune('A' + i))
		chunks = append(chunks, Chunk{ID: id, DocumentID: id, Title: "Redis 张三", Text: "13800138000 Redis queue"})
	}
	s := Service{Options: Options{Rerank: &cfg, MaskName: "张三"}, Provider: p}
	out, e := s.Rank(context.Background(), "u", "Redis 张三 13800138000", chunks, nil, 5)
	if e != nil || documents != 20 || len(out.Hits) != 5 || out.Retrieval.Mode != "keyword+rerank" || out.Hits[0].RerankScore == nil {
		t.Fatal("rerank bound/mapping", out, e, documents)
	}
	p, cfg = testProvider(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	s.Options = Options{Embedding: &cfg}
	s.Provider = p
	vectors := map[string]SemanticVector{chunks[0].ID: NewSemanticVector(chunks[0], []float64{1, 0}, "")}
	out, e = s.Rank(context.Background(), "u", "Redis", chunks, vectors, 5)
	if e != nil || len(out.Hits) == 0 || out.Retrieval.Mode != "keyword" || len(out.Retrieval.Warnings) == 0 {
		t.Fatal("fallback", out, e)
	}
}

func TestRerankerRejectsMissingDuplicateAndOutOfRangeIndices(t *testing.T) {
	for _, body := range []string{`{"results":[{"index":0,"relevance_score":0.5}]}`, `{"results":[{"index":0,"relevance_score":0.5},{"index":0,"relevance_score":0.9}]}`, `{"results":[{"index":0,"relevance_score":0.5},{"index":3,"relevance_score":0.9}]}`, `{"results":[{"index":0,"relevance_score":0.5},{"index":1,"relevance_score":1.9}]}`} {
		p, cfg := testProvider(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) })
		if _, e := p.Rerank(context.Background(), cfg, "q", []string{"a", "b"}); e == nil {
			t.Fatal("invalid ranking accepted", body)
		}
	}
}
func TestMetricsFailedAndUnreviewedDenominators(t *testing.T) {
	dataset := Dataset{Name: "human test", Kind: "human-labeled", Documents: []EvalDocument{{ID: "a", Title: "A", Text: "A"}, {ID: "b", Title: "B", Text: "B"}}, Queries: []EvalQuery{{ID: "q1", Query: "pass", Reviewed: true, Relevant: map[string]int{"a": 3}}, {ID: "q2", Query: "fail", Reviewed: true, Relevant: map[string]int{"a": 3}}, {ID: "q3", Query: "unreviewed", Reviewed: false, Relevant: map[string]int{"b": 3}}}}
	report, e := Evaluate(context.Background(), dataset, "keyword", func(ctx context.Context, q string) (SearchResult, error) {
		if q == "fail" {
			return SearchResult{}, errors.New("provider failure")
		}
		return SearchResult{Hits: []Hit{{Chunk: Chunk{DocumentID: "a"}}}}, nil
	})
	if e != nil || report.Labeled != 2 || report.Failed != 1 || *report.Recall20 != .5 || *report.NDCG5 != .5 || report.Rows[2].NDCG5 != nil {
		t.Fatal(report, e)
	}
	a, b, c := retrievalMetrics([]string{"b", "a", "a"}, map[string]int{"a": 3})
	if a != 1 || b >= 1 || c != .5 {
		t.Fatal("duplicate doc or rank calculation", a, b, c)
	}
}
