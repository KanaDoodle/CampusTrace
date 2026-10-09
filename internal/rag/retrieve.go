package rag

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"
)

type SearchResult struct {
	Hits      []Hit         `json:"hits"`
	Retrieval RetrievalInfo `json:"retrieval"`
}
type RetrievalInfo struct {
	Mode           string   `json:"mode"`
	Version        string   `json:"version"`
	TotalChunks    int      `json:"total_chunks"`
	IndexedChunks  int      `json:"indexed_chunks"`
	Candidates     int      `json:"candidates"`
	EmbeddingCalls int      `json:"embedding_calls"`
	RerankCalls    int      `json:"rerank_calls"`
	QueryCacheHit  bool     `json:"query_cache_hit"`
	Warnings       []string `json:"warnings"`
	Milliseconds   int64    `json:"milliseconds"`
	Usage          Usage    `json:"usage"`
}
type SemanticVector struct {
	ChunkID   string    `json:"chunk_id"`
	InputHash string    `json:"input_hash"`
	Vector    []float64 `json:"vector"`
}

// Corpus-aware BM25 retains literal technology names. Hash-vector collisions
// no longer affect keyword ranking; Chinese uses the existing bigram tokens.
func KeywordRank(query string, chunks []Chunk) []Hit {
	q := map[string]bool{}
	for _, w := range Tokens(query) {
		q[w] = true
	}
	counts := make([]map[string]int, len(chunks))
	lengths := make([]int, len(chunks))
	df := map[string]int{}
	avg := 0.0
	for i, c := range chunks {
		counts[i] = map[string]int{}
		for _, w := range Tokens(c.Title + " " + c.Text) {
			counts[i][w]++
			lengths[i]++
		}
		avg += float64(lengths[i])
		for w := range q {
			if counts[i][w] > 0 {
				df[w]++
			}
		}
	}
	if avg == 0 {
		return []Hit{}
	}
	avg /= float64(len(chunks))
	hits := []Hit{}
	for i, c := range chunks {
		score := 0.0
		for w := range q {
			tf := float64(counts[i][w])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (float64(len(chunks)-df[w])+.5)/(float64(df[w])+.5))
			score += idf * tf * 2.2 / (tf + 1.2*(.25+.75*float64(lengths[i])/avg))
		}
		if score > 0 {
			c.Vector = nil
			hits = append(hits, Hit{Chunk: c, Score: score, Keyword: score})
		}
	}
	sortHits(hits)
	return hits
}
func sortHits(hits []Hit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ID < hits[j].ID
		}
		return hits[i].Score > hits[j].Score
	})
}
func SemanticRank(chunks []Chunk, vectors map[string]SemanticVector, query []float64, mask string) []Hit {
	hits := []Hit{}
	for _, c := range chunks {
		v, ok := vectors[c.ID]
		if !ok || v.InputHash != inputHash(c, mask) || !validVector(v.Vector) || len(v.Vector) != len(query) {
			continue
		}
		cos := Cosine(query, v.Vector)
		if cos < .25 {
			continue
		}
		c.Vector = nil
		hits = append(hits, Hit{Chunk: c, Score: cos, Cosine: cos})
	}
	sortHits(hits)
	return hits
}

// Each branch contributes its rank, so BM25 and cosine need no arbitrary
// shared score scale. Candidates are bounded before any paid reranking call.
func Fuse(keyword, semantic []Hit, limit int) []Hit {
	merged := map[string]Hit{}
	for _, branch := range [][]Hit{keyword, semantic} {
		for i, h := range branch {
			if i >= 40 {
				break
			}
			old, ok := merged[h.ID]
			if !ok {
				old = h
				old.Score = 0
			}
			old.Score += 1 / float64(60+i+1)
			old.Keyword = math.Max(old.Keyword, h.Keyword)
			old.Cosine = math.Max(old.Cosine, h.Cosine)
			merged[h.ID] = old
		}
	}
	out := []Hit{}
	for _, h := range merged {
		out = append(out, h)
	}
	sortHits(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
func ApplyRerank(hits []Hit, scores []RerankScore) []Hit {
	out := append([]Hit{}, hits...)
	for _, v := range scores {
		out[v.Index].Score = v.Score
		score := v.Score
		out[v.Index].RerankScore = &score
	}
	sortHits(out)
	return out
}

type cachedQuery struct {
	Vector []float64
	Until  time.Time
}
type QueryCache struct {
	mu      sync.Mutex
	entries map[string]cachedQuery
}

func (c *QueryCache) get(key string) ([]float64, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[key]
	if !ok || !v.Until.After(time.Now()) {
		delete(c.entries, key)
		return nil, false
	}
	return append([]float64{}, v.Vector...), true
}
func (c *QueryCache) put(key string, vector []float64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]cachedQuery{}
	}
	if len(c.entries) >= 256 {
		for k, v := range c.entries {
			if !v.Until.After(time.Now()) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= 256 {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
	}
	c.entries[key] = cachedQuery{append([]float64{}, vector...), time.Now().Add(5 * time.Minute)}
}

// Rank is also used by offline/live evaluation. Retrieval errors are visible
// fallbacks, not invented relevance scores or silently retried paid calls.
func (s *Service) Rank(ctx context.Context, user, query string, chunks []Chunk, vectors map[string]SemanticVector, k int) (SearchResult, error) {
	start := time.Now()
	info := RetrievalInfo{Mode: "keyword", Version: RetrievalVersion, TotalChunks: len(chunks), Warnings: []string{}}
	keyword := KeywordRank(query, chunks)
	hits := keyword
	if s.Options.Embedding != nil {
		info.IndexedChunks = validIndexed(chunks, vectors, s.Options.MaskName)
		if info.IndexedChunks == 0 {
			info.Warnings = append(info.Warnings, "RETRIEVAL_INDEX_REQUIRED")
		} else {
			safeQuery := Redact(query, s.Options.MaskName)
			key := user + ":" + modelKey(s.Options.Embedding) + ":" + hash(safeQuery)
			q, ok := s.Cache.get(key)
			info.QueryCacheHit = ok
			var err error
			if !ok {
				info.EmbeddingCalls++
				var vs [][]float64
				vs, info.Usage, err = s.Provider.Embed(ctx, *s.Options.Embedding, []string{safeQuery})
				if err == nil {
					q = vs[0]
					s.Cache.put(key, q)
				}
			}
			if err != nil {
				if ctx.Err() != nil {
					return SearchResult{}, ctx.Err()
				}
				info.Warnings = append(info.Warnings, failureCode(err))
			} else {
				// Never compare vector spaces or dimensions from different models.
				consistent := true
				for _, c := range chunks {
					if v, ok := vectors[c.ID]; ok && v.InputHash == inputHash(c, s.Options.MaskName) && validVector(v.Vector) && len(v.Vector) != len(q) {
						consistent = false
						break
					}
				}
				if !consistent {
					info.Warnings = append(info.Warnings, "RETRIEVAL_DIMENSION_CHANGED")
				} else {
					hits = Fuse(keyword, SemanticRank(chunks, vectors, q, s.Options.MaskName), 20)
					info.Mode = "hybrid"
				}
			}
			if info.IndexedChunks < len(chunks) {
				info.Warnings = append(info.Warnings, "RETRIEVAL_INDEX_PARTIAL")
			}
		}
	}
	if len(hits) > 20 {
		hits = hits[:20]
	}
	info.Candidates = len(hits)
	if s.Options.Rerank != nil && len(hits) > 1 {
		texts := []string{}
		for _, h := range hits {
			texts = append(texts, inputText(h.Chunk, s.Options.MaskName))
		}
		info.RerankCalls++
		scores, err := s.Provider.Rerank(ctx, *s.Options.Rerank, Redact(query, s.Options.MaskName), texts)
		if err != nil {
			if ctx.Err() != nil {
				return SearchResult{}, ctx.Err()
			}
			info.Warnings = append(info.Warnings, failureCode(err))
		} else {
			hits = ApplyRerank(hits, scores)
			info.Mode += "+rerank"
		}
	}
	if len(hits) > k {
		hits = hits[:k]
	}
	if hits == nil {
		hits = []Hit{}
	}
	info.Milliseconds = time.Since(start).Milliseconds()
	return SearchResult{hits, info}, nil
}

func NewSemanticVector(c Chunk, vector []float64, mask string) SemanticVector {
	return SemanticVector{c.ID, inputHash(c, mask), vector}
}
