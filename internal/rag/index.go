package rag

import (
	"context"
	"database/sql"
	"errors"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func hash(s string) string                  { return d.Hash(s) }
func inputHash(c Chunk, mask string) string { return hash(inputText(c, mask)) }
func validIndexed(chunks []Chunk, vectors map[string]SemanticVector, mask string) int {
	n := 0
	for _, c := range chunks {
		if v, ok := vectors[c.ID]; ok && v.InputHash == inputHash(c, mask) && validVector(v.Vector) {
			n++
		}
	}
	return n
}
func bumpKnowledge(ctx context.Context, tx *sql.Tx, user string) error {
	_, e := tx.ExecContext(ctx, "INSERT INTO knowledge_state(user_id,revision) VALUES(?,1) ON DUPLICATE KEY UPDATE revision=revision+1", user)
	return e
}

type IndexInput struct {
	ChunkID string `json:"chunk_id"`
	Title   string `json:"title"`
	Text    string `json:"text"`
}
type IndexPreview struct {
	Key     string       `json:"key"`
	Total   int          `json:"total"`
	Indexed int          `json:"indexed"`
	Pending int          `json:"pending"`
	Inputs  []IndexInput `json:"inputs"`
	Model   string       `json:"model"`
	URL     string       `json:"url"`
}

func (s *Service) PreviewIndex(ctx context.Context, user string) (IndexPreview, error) {
	out := IndexPreview{Inputs: []IndexInput{}}
	if s.Options.Validate() != nil || s.Options.Embedding == nil {
		return out, p.ErrValidation
	}
	chunks, e := s.chunks(ctx, user)
	if e != nil {
		return out, e
	}
	rows, e := p.Many[SemanticVector](ctx, s.Store.DB, "SELECT body FROM knowledge_vectors WHERE user_id=? AND model_key=? ORDER BY chunk_id LIMIT 10001", user, modelKey(s.Options.Embedding))
	if e != nil {
		return out, e
	}
	vectors := map[string]SemanticVector{}
	for _, v := range rows {
		vectors[v.ChunkID] = v
	}
	out.Total = len(chunks)
	out.Indexed = validIndexed(chunks, vectors, s.Options.MaskName)
	out.Pending = out.Total - out.Indexed
	out.Model = s.Options.Embedding.Model
	out.URL = s.Options.Embedding.URL
	bytes := 0
	for _, c := range chunks {
		if v, ok := vectors[c.ID]; ok && v.InputHash == inputHash(c, s.Options.MaskName) && validVector(v.Vector) {
			continue
		}
		text := inputText(c, s.Options.MaskName)
		if len(out.Inputs) >= MaxIndexBatch || bytes+len(text) > 100000 {
			break
		}
		out.Inputs = append(out.Inputs, IndexInput{c.ID, Redact(c.Title, s.Options.MaskName), text})
		bytes += len(text)
	}
	out.Key = hash(user + modelKey(s.Options.Embedding) + d.JSON(out.Inputs))
	return out, nil
}

type IndexResult struct {
	Indexed int   `json:"indexed"`
	Usage   Usage `json:"usage"`
}

// A per-account lease prevents two tabs from paying to index the same batch.
// Network calls run outside SQL transactions. Input and lease are checked
// again under the user lock before vectors become visible atomically.
func (s *Service) Index(ctx context.Context, user, key string) (IndexResult, error) {
	out := IndexResult{}
	if e := s.acquire(ctx); e != nil {
		return out, e
	}
	defer s.release()
	token := d.ID()
	e := s.Store.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if e := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&id); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "INSERT IGNORE INTO knowledge_state(user_id) VALUES(?)", user); e != nil {
			return e
		}
		result, e := tx.ExecContext(ctx, "UPDATE knowledge_state SET lease_key=?,lease_until=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND WHERE user_id=? AND (lease_until IS NULL OR lease_until<=UTC_TIMESTAMP(6))", token, user)
		if e != nil {
			return e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return p.ErrConflict
		}
		return nil
	})
	if e != nil {
		return out, e
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		s.Store.DB.ExecContext(cleanup, "UPDATE knowledge_state SET lease_key='',lease_until=NULL WHERE user_id=? AND lease_key=?", user, token)
	}()
	preview, e := s.PreviewIndex(ctx, user)
	if e != nil {
		return out, e
	}
	if key != preview.Key {
		return out, p.ErrStaleInput
	}
	if len(preview.Inputs) == 0 {
		return out, nil
	}
	var count int
	if e = s.Store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_vectors WHERE user_id=?", user).Scan(&count); e != nil {
		return out, e
	}
	if count+len(preview.Inputs) > 2*SearchCapacity {
		return out, providerError("RETRIEVAL_CACHE_CAPACITY")
	}
	texts := []string{}
	for _, v := range preview.Inputs {
		texts = append(texts, v.Text)
	}
	vectors, usage, e := s.Provider.Embed(ctx, *s.Options.Embedding, texts)
	if e != nil {
		return out, e
	}
	e = s.Store.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if e := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&id); e != nil {
			return e
		}
		var lease string
		var valid bool
		if e := tx.QueryRowContext(ctx, "SELECT lease_key,COALESCE(lease_until>UTC_TIMESTAMP(6),FALSE) FROM knowledge_state WHERE user_id=? FOR UPDATE", user).Scan(&lease, &valid); e != nil {
			return e
		}
		if lease != token || !valid {
			return p.ErrStaleInput
		}
		// An endpoint returning a new dimension for the same model cannot silently
		// leave the old and new documents in one incompatible vector space.
		var existing []byte
		e := tx.QueryRowContext(ctx, "SELECT body FROM knowledge_vectors WHERE user_id=? AND model_key=? LIMIT 1", user, modelKey(s.Options.Embedding)).Scan(&existing)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			var v SemanticVector
			if d.StrictLimit(existing, &v, 1<<20) != nil || len(v.Vector) != len(vectors[0]) {
				return providerError("RETRIEVAL_DIMENSION_CHANGED")
			}
		}
		for i, v := range preview.Inputs {
			c, e := p.One[Chunk](ctx, tx, "SELECT body FROM chunks WHERE id=? AND user_id=? FOR UPDATE", v.ChunkID, user)
			if e != nil {
				return p.ErrStaleInput
			}
			if inputText(c, s.Options.MaskName) != v.Text {
				return p.ErrStaleInput
			}
			vector := SemanticVector{c.ID, inputHash(c, s.Options.MaskName), vectors[i]}
			if _, e = tx.ExecContext(ctx, "INSERT INTO knowledge_vectors(user_id,chunk_id,model_key,input_hash,body,created_at) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6)) ON DUPLICATE KEY UPDATE input_hash=VALUES(input_hash),body=VALUES(body),created_at=VALUES(created_at)", user, c.ID, modelKey(s.Options.Embedding), vector.InputHash, d.JSON(vector)); e != nil {
				return e
			}
		}
		return bumpKnowledge(ctx, tx, user)
	})
	if e != nil {
		return out, e
	}
	return IndexResult{len(vectors), usage}, nil
}

type DocumentSummary struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	Chunks    int       `json:"chunks"`
}

func (s *Service) Documents(ctx context.Context, user string, offset int) ([]DocumentSummary, error) {
	if offset < 0 || offset > SearchCapacity {
		return nil, p.ErrValidation
	}
	rows, e := s.Store.DB.QueryContext(ctx, "SELECT d.id,JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.title')),JSON_UNQUOTE(JSON_EXTRACT(d.body,'$.created_at')),(SELECT COUNT(*) FROM chunks c WHERE c.document_id=d.id AND c.user_id=d.user_id) FROM documents d WHERE d.user_id=? ORDER BY d.id LIMIT 51 OFFSET ?", user, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []DocumentSummary{}
	for rows.Next() {
		var v DocumentSummary
		var stamp string
		if e = rows.Scan(&v.ID, &v.Title, &stamp, &v.Chunks); e != nil {
			return nil, e
		}
		v.CreatedAt, _ = time.Parse(time.RFC3339Nano, stamp)
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) DeleteDocument(ctx context.Context, user, id string) error {
	return s.Store.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if e := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); e != nil {
			return e
		}
		if _, e := p.One[Document](ctx, tx, "SELECT body FROM documents WHERE id=? AND user_id=? FOR UPDATE", id, user); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM chunks WHERE user_id=? AND document_id=?", user, id); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM documents WHERE user_id=? AND id=?", user, id); e != nil {
			return e
		}
		return bumpKnowledge(ctx, tx, user)
	})
}
func (s *Service) ClearVectors(ctx context.Context, user string) error {
	return s.Store.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if e := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "DELETE FROM knowledge_vectors WHERE user_id=?", user); e != nil {
			return e
		}
		if _, e := tx.ExecContext(ctx, "UPDATE knowledge_state SET lease_key='',lease_until=NULL WHERE user_id=?", user); e != nil {
			return e
		}
		return bumpKnowledge(ctx, tx, user)
	})
}
