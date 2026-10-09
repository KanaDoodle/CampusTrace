package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"strings"
	"time"
	"unicode/utf8"
)

type MemoryInput struct {
	ID      string `json:"id,omitempty"`
	Version uint64 `json:"version,omitempty"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
	Scope   string `json:"scope,omitempty"`
	Days    int    `json:"days,omitempty"`
}

func (v MemoryInput) Validate() error {
	if v.Kind != "PREFERENCE" && v.Kind != "DECISION" && v.Kind != "CORRECTION" {
		return ErrValidation
	}
	if strings.TrimSpace(v.Content) == "" || len(v.Content) > 2000 || len(v.Scope) > 200 || !utf8.ValidString(v.Content) || !utf8.ValidString(v.Scope) || strings.ContainsRune(v.Scope, '\x00') || strings.ContainsRune(v.Content, '\x00') || resume.HasSensitive(v.Content) || resume.HasSensitive(v.Scope) || v.Days < 0 || v.Days > 3650 {
		return ErrValidation
	}
	if v.ID != "" && (len(v.ID) != 32 || v.Version == 0) {
		return ErrValidation
	}
	return nil
}

type AgentMemory struct {
	MemoryInput
	UserID    string    `json:"-"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func memoryRevision(ctx context.Context, q Queryer, user string) (uint64, error) {
	var n uint64
	err := q.QueryRowContext(ctx, "SELECT revision FROM agent_memory_state WHERE user_id=?", user).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}
func (s *Store) MemoryRevision(ctx context.Context, user string) (uint64, error) {
	return memoryRevision(ctx, s.DB, user)
}
func (s *Store) AgentMemories(ctx context.Context, user string) ([]AgentMemory, error) {
	return Many[AgentMemory](ctx, s.DB, "SELECT body FROM agent_memories WHERE user_id=? AND deleted=FALSE AND expires_at>UTC_TIMESTAMP(6) ORDER BY updated_at DESC,id LIMIT 100", user)
}
func (s *Store) SearchMemories(ctx context.Context, user, query string) ([]AgentMemory, error) {
	rows, err := s.AgentMemories(ctx, user)
	if err != nil {
		return nil, err
	}
	out := []AgentMemory{}
	q := strings.ToLower(strings.TrimSpace(query))
	for _, v := range rows {
		if q == "" || strings.Contains(strings.ToLower(v.Content+" "+v.Scope), q) {
			out = append(out, v)
			if len(out) == 8 {
				break
			}
		}
	}
	return out, nil
}
func saveMemoryTx(ctx context.Context, tx *sql.Tx, user string, in MemoryInput) (AgentMemory, error) {
	var out AgentMemory
	if err := in.Validate(); err != nil {
		return out, err
	}
	now := time.Now().UTC()
	if in.Days == 0 {
		in.Days = 180
	}
	if in.ID != "" {
		old, err := One[AgentMemory](ctx, tx, "SELECT body FROM agent_memories WHERE id=? AND user_id=? AND deleted=FALSE FOR UPDATE", in.ID, user)
		if err != nil {
			return out, err
		}
		if old.Version != in.Version {
			return out, ErrConflict
		}
		in.Version++
	} else {
		existing, err := Many[AgentMemory](ctx, tx, "SELECT body FROM agent_memories WHERE user_id=? AND deleted=FALSE AND expires_at>? FOR UPDATE", user, now)
		if err != nil {
			return out, err
		}
		for _, v := range existing {
			if v.Kind == in.Kind && v.Content == in.Content && v.Scope == in.Scope {
				return v, nil
			}
		}
		if len(existing) >= 100 {
			return out, ErrValidation
		}
		in.ID = d.ID()
		in.Version = 1
	}
	out = AgentMemory{MemoryInput: in, UserID: user, UpdatedAt: now, ExpiresAt: now.Add(time.Duration(in.Days) * 24 * time.Hour)}
	_, err := tx.ExecContext(ctx, "INSERT INTO agent_memories(id,user_id,version,expires_at,updated_at,body) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE version=VALUES(version),expires_at=VALUES(expires_at),updated_at=VALUES(updated_at),body=VALUES(body)", out.ID, user, out.Version, out.ExpiresAt, now, d.JSON(out))
	if err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO agent_memory_state(user_id,revision) VALUES(?,1) ON DUPLICATE KEY UPDATE revision=revision+1", user)
	return out, err
}
func (s *Store) SaveMemory(ctx context.Context, user string, in MemoryInput) (AgentMemory, error) {
	var out AgentMemory
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		var e error
		out, e = saveMemoryTx(ctx, tx, user, in)
		return e
	})
	return out, err
}
func (s *Store) ForgetMemory(ctx context.Context, user, id string, version uint64) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		old, err := One[AgentMemory](ctx, tx, "SELECT body FROM agent_memories WHERE id=? AND user_id=? AND deleted=FALSE FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		if version != old.Version {
			return ErrConflict
		}
		// Redact confirmed action snapshots too, while retaining their nonce receipts
		// so replaying an old confirmation cannot resurrect the forgotten note.
		if _, err = tx.ExecContext(ctx, `UPDATE action_receipts SET result=JSON_SET(result,'$.content','','$.scope','','$.forgotten',TRUE) WHERE user_id=? AND JSON_UNQUOTE(JSON_EXTRACT(result,'$.id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(result,'$.kind')) IN ('PREFERENCE','DECISION','CORRECTION')`, user, id); err != nil {
			return err
		}
		// Erase content as well as hiding the record. Epoch invalidates old contexts.
		if _, err = tx.ExecContext(ctx, "DELETE FROM agent_tasks WHERE user_id=?", user); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE agent_executions SET state='STALE',lease_token='',lease_until=NULL,body=JSON_SET(body,'$.state','STALE','$.input',NULL,'$.plan',JSON_ARRAY(),'$.grounded_observations',JSON_ARRAY(),'$.failures',JSON_ARRAY(),'$.answer','') WHERE user_id=?`, user); err != nil {
			return err
		}
		old.Content = ""
		old.Scope = ""
		old.Version++
		old.UpdatedAt = time.Now().UTC()
		_, err = tx.ExecContext(ctx, "UPDATE agent_memories SET deleted=TRUE,version=?,updated_at=?,body=? WHERE id=? AND user_id=?", old.Version, old.UpdatedAt, d.JSON(old), id, user)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO agent_memory_state(user_id,revision) VALUES(?,1) ON DUPLICATE KEY UPDATE revision=revision+1", user)
		return err
	})
}

type AgentTask struct {
	ID                string    `json:"id"`
	ParentID          string    `json:"parent_id,omitempty"`
	Goal              string    `json:"goal"`
	Summary           string    `json:"summary"`
	State             string    `json:"state"`
	CandidateHash     string    `json:"candidate_hash"`
	MemoryRevision    uint64    `json:"memory_revision"`
	MemoryHash        string    `json:"memory_hash"`
	KnowledgeRevision string    `json:"knowledge_revision,omitempty"`
	RetrievalIdentity string    `json:"retrieval_identity,omitempty"`
	ModelCalls        int       `json:"model_calls"`
	ToolCalls         int       `json:"tool_calls"`
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

func (s *Store) AgentTask(ctx context.Context, user, id string) (AgentTask, error) {
	return One[AgentTask](ctx, s.DB, "SELECT body FROM agent_tasks WHERE id=? AND user_id=? AND expires_at>UTC_TIMESTAMP(6)", id, user)
}
func (s *Store) AgentTasks(ctx context.Context, user string) ([]AgentTask, error) {
	return Many[AgentTask](ctx, s.DB, "SELECT body FROM agent_tasks WHERE user_id=? AND expires_at>UTC_TIMESTAMP(6) ORDER BY created_at DESC,id LIMIT 20", user)
}
func (s *Store) SaveAgentTask(ctx context.Context, user string, v AgentTask) error {
	if len(v.ID) != 32 || len(v.Goal) > 4000 || len(v.Summary) > 16000 {
		return ErrValidation
	}
	// Run records are immutable: concurrent conversations never overwrite one another.
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		epoch, e := memoryRevision(ctx, tx, user)
		if e != nil {
			return e
		}
		if epoch != v.MemoryRevision {
			return ErrConflict
		}
		if v.KnowledgeRevision != "" {
			var revision uint64
			err := tx.QueryRowContext(ctx, "SELECT revision FROM knowledge_state WHERE user_id=?", user).Scan(&revision)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if fmt.Sprint(revision) != v.KnowledgeRevision {
				return ErrConflict
			}
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM agent_tasks WHERE user_id=? AND expires_at<=UTC_TIMESTAMP(6)", user); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO agent_tasks(id,user_id,created_at,expires_at,body) VALUES(?,?,?,?,?)", v.ID, user, v.CreatedAt, v.ExpiresAt, d.JSON(v))
		return e
	})
}

// A repeatable snapshot binds every context to the same profile and memory state.
func (s *Store) AgentContext(ctx context.Context, user string) (revision uint64, rows []AgentMemory, profileHash string, err error) {
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if e != nil {
		err = e
		return
	}
	defer tx.Rollback()
	revision, err = memoryRevision(ctx, tx, user)
	if err != nil {
		return
	}
	rows, err = Many[AgentMemory](ctx, tx, "SELECT body FROM agent_memories WHERE user_id=? AND deleted=FALSE AND expires_at>UTC_TIMESTAMP(6) ORDER BY updated_at DESC,id LIMIT 100", user)
	if err != nil {
		return
	}
	_, c, e := matchCandidate(ctx, tx, user, "")
	if e != nil && !errors.Is(e, ErrNotFound) {
		err = e
		return
	}
	if e == nil {
		profileHash = c.Hash()
	}
	err = tx.Commit()
	return
}
