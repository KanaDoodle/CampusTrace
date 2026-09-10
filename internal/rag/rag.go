package rag

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

const SearchCapacity = 10000

var ErrCapacity = errors.New("CORPUS_CAPACITY: searchable capacity of 10000 chunks exceeded")

const EmbeddingVersion = "lexical-hash-128-v1"

type Document struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}
type Chunk struct {
	ID               string    `json:"id"`
	DocumentID       string    `json:"document_id"`
	Title            string    `json:"title"`
	Text             string    `json:"text"`
	Vector           []float64 `json:"embedding"`
	EmbeddingVersion string    `json:"embedding_version"`
	Index            int       `json:"index"`
}
type Hit struct {
	Chunk
	Score   float64 `json:"score"`
	Cosine  float64 `json:"cosine"`
	Keyword float64 `json:"keyword"`
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
func Embed(text string) []float64 {
	v := make([]float64, 128)
	for _, token := range Tokens(text) {
		h := fnv.New32a()
		h.Write([]byte(token))
		v[h.Sum32()%128]++
	}
	return v
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
func Score(query string, c Chunk) Hit {
	q := map[string]bool{}
	for _, w := range Tokens(query) {
		q[w] = true
	}
	words := map[string]bool{}
	for _, w := range Tokens(c.Title + " " + c.Text) {
		words[w] = true
	}
	n := 0
	for w := range q {
		if words[w] {
			n++
		}
	}
	keyword := 0.0
	if len(q) > 0 {
		keyword = float64(n) / float64(len(q))
	}
	cos := Cosine(Embed(query), c.Vector)
	return Hit{Chunk: c, Score: 0.55*keyword + 0.45*cos, Cosine: cos, Keyword: keyword}
}

type Service struct {
	Store *p.Store
	Sem   chan struct{}
	Allow func(context.Context) (bool, error)
}

func (s *Service) Ingest(ctx context.Context, user string, doc Document) (Document, error) {
	if doc.Title == "" || len(doc.Title) > 200 || len(doc.Text) == 0 || len(doc.Text) > 60000 {
		return doc, errors.New("invalid document")
	}
	select {
	case s.Sem <- struct{}{}:
		defer func() { <-s.Sem }()
	case <-ctx.Done():
		return doc, ctx.Err()
	}
	if s.Allow != nil {
		ok, err := s.Allow(ctx)
		if err != nil {
			return doc, err
		}
		if !ok {
			return doc, errors.New("429 embedding rate limit")
		}
	}
	doc.ID = d.ID()
	doc.CreatedAt = time.Now().UTC()
	err := s.Store.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks WHERE user_id=?", user).Scan(&count); err != nil {
			return err
		}
		if count+(len([]rune(doc.Text))+699)/700 > SearchCapacity {
			return ErrCapacity
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO documents(id,user_id,body) VALUES(?,?,?)", doc.ID, user, d.JSON(doc))
		if err != nil {
			return err
		}
		runes := []rune(doc.Text)
		for start, index := 0, 0; start < len(runes); start, index = start+700, index+1 {
			end := start + 900
			if end > len(runes) {
				end = len(runes)
			}
			text := string(runes[start:end])
			c := Chunk{ID: d.ID(), DocumentID: doc.ID, Title: doc.Title, Text: text, Vector: Embed(doc.Title + " " + text), EmbeddingVersion: EmbeddingVersion, Index: index}
			_, err = tx.ExecContext(ctx, "INSERT INTO chunks(id,user_id,document_id,body) VALUES(?,?,?,?)", c.ID, user, doc.ID, d.JSON(c))
			if err != nil {
				return err
			}
		}
		return nil
	})
	return doc, err
}
func (s *Service) Search(ctx context.Context, user, query string, k int) ([]Hit, error) {
	if len(query) == 0 || len(query) > 1000 || k < 1 || k > 20 {
		return nil, errors.New("invalid knowledge query")
	}
	select {
	case s.Sem <- struct{}{}:
		defer func() { <-s.Sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.Allow != nil {
		ok, err := s.Allow(ctx)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("429 embedding rate limit")
		}
	}
	chunks, err := p.Many[Chunk](ctx, s.Store.DB, "SELECT body FROM chunks WHERE user_id=? ORDER BY id LIMIT 10001", user)
	if err != nil {
		return nil, err
	}
	if len(chunks) > SearchCapacity {
		return nil, ErrCapacity
	}
	hits := []Hit{}
	for _, c := range chunks {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if c.EmbeddingVersion != EmbeddingVersion {
			continue
		}
		h := Score(query, c)
		if h.Keyword > 0 {
			hits = append(hits, h)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].ID < hits[j].ID
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}
