package rag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"math"
	"strings"
	"time"
	"unicode"
)

const SearchCapacity = 10000

var ErrCapacity = errors.New("CORPUS_CAPACITY: searchable capacity of 10000 chunks exceeded")

// Retained as the chunk-format tag for existing data. New chunks omit the
// obsolete hash vector; real semantic vectors live in knowledge_vectors.
const EmbeddingVersion = "lexical-hash-128-v1"

type Document struct {
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	Text      string          `json:"text"`
	CreatedAt time.Time       `json:"created_at"`
	Origin    *DocumentOrigin `json:"origin,omitempty"`
}
type Chunk struct {
	ID               string    `json:"id"`
	DocumentID       string    `json:"document_id"`
	Title            string    `json:"title"`
	Text             string    `json:"text"`
	Vector           []float64 `json:"embedding,omitempty"`
	EmbeddingVersion string    `json:"embedding_version"`
	Index            int       `json:"index"`
}
type Hit struct {
	Chunk
	Score       float64  `json:"score"`
	Cosine      float64  `json:"cosine"`
	Keyword     float64  `json:"keyword"`
	RerankScore *float64 `json:"rerank_score,omitempty"`
}

func Tokens(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := []string{}
	for _, w := range words {
		rs := []rune(w)
		if len(rs) > 1 && unicode.Is(unicode.Han, rs[0]) {
			for i := 0; i < len(rs)-1; i++ {
				out = append(out, string(rs[i:i+2]))
			}
		} else {
			out = append(out, w)
		}
	}
	return out
}
func Cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	dot, an, bn := 0.0, 0.0, 0.0
	for i, x := range a {
		dot += x * b[i]
		an += x * x
		bn += b[i] * b[i]
	}
	if an == 0 || bn == 0 {
		return 0
	}
	return dot / math.Sqrt(an*bn)
}

type Service struct {
	Store    *p.Store
	Sem      chan struct{}
	Allow    func(context.Context) (bool, error)
	Options  Options
	Provider Provider
	Cache    *QueryCache
}

func (s *Service) Ingest(ctx context.Context, user string, doc Document) (Document, error) {
	// Compatibility API keeps its full Document result while sharing import
	// deduplication, ownership and corpus revision behavior.
	out, _, err := s.ImportDocument(ctx, user, doc)
	return out, err
}

// ImportDocument reuses an exact title/body match under the user's corpus lock.
// A lost response can therefore be retried without adding another document.
func (s *Service) ImportDocument(ctx context.Context, user string, doc Document) (Document, bool, error) {
	if doc.Origin != nil {
		return Document{}, false, p.ErrValidation
	}
	return s.ingest(ctx, user, doc, nil)
}

func (s *Service) ingest(ctx context.Context, user string, doc Document, source *reviewReference) (Document, bool, error) {
	if strings.TrimSpace(doc.Title) == "" || len(doc.Title) > 200 || strings.TrimSpace(doc.Text) == "" || len(doc.Text) > 60000 {
		return doc, false, errors.New("invalid document")
	}
	if err := s.acquire(ctx); err != nil {
		return doc, false, err
	}
	defer s.release()
	if s.Allow != nil {
		ok, err := s.Allow(ctx)
		if err != nil {
			return doc, false, err
		}
		if !ok {
			return doc, false, errors.New("429 embedding rate limit")
		}
	}
	doc.ID = d.ID()
	doc.CreatedAt = time.Now().UTC()
	parts := splitText(doc.Text)
	reused := false
	err := s.Store.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
			return err
		}
		originKey := ""
		if source != nil {
			interview, review, err := reviewSource(ctx, tx, user, source.InterviewID)
			if err != nil {
				return err
			}
			origin := reviewOrigin(user, interview, review)
			if origin.SourceKey != source.Key {
				return p.ErrStaleInput
			}
			doc.Origin = &origin
			originKey = origin.SourceKey
		}
		var body []byte
		err := tx.QueryRowContext(ctx, "SELECT body FROM documents WHERE user_id=? AND BINARY JSON_UNQUOTE(JSON_EXTRACT(body,'$.title'))=BINARY ? AND BINARY JSON_UNQUOTE(JSON_EXTRACT(body,'$.text'))=BINARY ? AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.origin.source_key')),'')=? LIMIT 1", user, doc.Title, doc.Text, originKey).Scan(&body)
		if err == nil {
			if err := json.Unmarshal(body, &doc); err != nil {
				return err
			}
			reused = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks WHERE user_id=?", user).Scan(&count); err != nil {
			return err
		}
		if count+len(parts) > SearchCapacity {
			return ErrCapacity
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO documents(id,user_id,body) VALUES(?,?,?)", doc.ID, user, d.JSON(doc))
		if err != nil {
			return err
		}
		for index, text := range parts {
			c := Chunk{ID: d.ID(), DocumentID: doc.ID, Title: doc.Title, Text: text, EmbeddingVersion: EmbeddingVersion, Index: index}
			_, err = tx.ExecContext(ctx, "INSERT INTO chunks(id,user_id,document_id,body) VALUES(?,?,?,?)", c.ID, user, doc.ID, d.JSON(c))
			if err != nil {
				return err
			}
		}
		return bumpKnowledge(ctx, tx, user)
	})
	return doc, reused, err
}
func (s *Service) Search(ctx context.Context, user, query string, k int) ([]Hit, error) {
	out, err := s.SearchDetailed(ctx, user, query, k)
	return out.Hits, err
}
func (s *Service) SearchDetailed(ctx context.Context, user, query string, k int) (SearchResult, error) {
	if strings.TrimSpace(query) == "" || len(query) > 1000 || k < 1 || k > 20 {
		return SearchResult{}, p.ErrValidation
	}
	if err := s.Options.Validate(); err != nil {
		return SearchResult{}, err
	}
	if err := s.acquire(ctx); err != nil {
		return SearchResult{}, err
	}
	defer s.release()
	revision, err := s.Store.KnowledgeIdentity(ctx, user)
	if err != nil {
		return SearchResult{}, err
	}
	chunks, err := s.chunks(ctx, user)
	if err != nil {
		return SearchResult{}, err
	}
	vectors := map[string]SemanticVector{}
	if s.Options.Embedding != nil {
		rows, e := p.Many[SemanticVector](ctx, s.Store.DB, "SELECT body FROM knowledge_vectors WHERE user_id=? AND model_key=? ORDER BY chunk_id LIMIT 10001", user, modelKey(s.Options.Embedding))
		if e != nil {
			return SearchResult{}, e
		}
		for _, v := range rows {
			vectors[v.ChunkID] = v
		}
	}
	out, err := s.Rank(ctx, user+":"+revision, query, chunks, vectors, k)
	if err != nil {
		return out, err
	}
	current, err := s.Store.KnowledgeIdentity(ctx, user)
	if err != nil {
		return SearchResult{}, err
	}
	if current != revision {
		return SearchResult{}, p.ErrStaleInput
	}
	out.Retrieval.CorpusVersion = revision
	return out, nil
}
func (s *Service) acquire(ctx context.Context) error {
	if s.Sem == nil {
		return nil
	}
	select {
	case s.Sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) release() {
	if s.Sem != nil {
		<-s.Sem
	}
}
func (s *Service) chunks(ctx context.Context, user string) ([]Chunk, error) {
	chunks, err := p.Many[Chunk](ctx, s.Store.DB, "SELECT body FROM chunks WHERE user_id=? ORDER BY id LIMIT 10001", user)
	if err != nil {
		return nil, err
	}
	if len(chunks) > SearchCapacity {
		return nil, ErrCapacity
	}
	out := []Chunk{}
	for _, c := range chunks {
		if c.EmbeddingVersion == EmbeddingVersion {
			out = append(out, c)
		}
	}
	return out, nil
}
