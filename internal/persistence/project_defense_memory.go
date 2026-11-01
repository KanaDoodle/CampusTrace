package persistence

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/projectdefense"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
)

// Clients identify an existing execution, never supply its answer or findings.
type SaveDefenseMemoryArgs struct {
	ID              string `json:"id,omitempty"`
	Version         uint64 `json:"version,omitempty"`
	ExecutionID     string `json:"execution_id"`
	Revision        int    `json:"revision"`
	ReviewHash      string `json:"review_hash"`
	EvidenceVersion string `json:"evidence_version"`
	Note            string `json:"note"`
	Disputed        bool   `json:"disputed"`
	Days            int    `json:"days"`
}

func (a SaveDefenseMemoryArgs) Validate() error {
	if len(a.ExecutionID) != 32 || a.Revision < 1 || len(a.ReviewHash) != 64 || len(a.EvidenceVersion) != 64 || len(a.Note) > 1500 || resume.HasSensitive(a.Note) || a.Days < 1 || a.Days > 365 || (a.ID != "" && (len(a.ID) != 32 || a.Version == 0)) || (a.ID == "" && a.Version != 0) {
		return ErrValidation
	}
	return nil
}

func (s *Store) DefenseMemories(ctx context.Context, user, topic string) ([]AgentMemory, error) {
	if _, ok := projectdefense.Find(topic); !ok {
		return nil, ErrValidation
	}
	return Many[AgentMemory](ctx, s.DB, `SELECT body FROM agent_memories WHERE user_id=? AND deleted=FALSE AND expires_at>UTC_TIMESTAMP(6) AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.kind'))='DEFENSE' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.episode.topic'))=? ORDER BY updated_at DESC,id LIMIT 100`, user, topic)
}

func (s *Store) DefenseMemory(ctx context.Context, user, id string) (AgentMemory, error) {
	return One[AgentMemory](ctx, s.DB, `SELECT body FROM agent_memories WHERE id=? AND user_id=? AND deleted=FALSE AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.kind'))='DEFENSE'`, id, user)
}

// Bounded topic recall; no inferred mastery, no use of disputed or old code.
// Identical selection is shared by the product and memory-on/off evaluation.
func SelectDefenseMemories(rows []AgentMemory, topic, version string, now time.Time) ([]d.DefenseMemory, int) {
	ordered := append([]AgentMemory{}, rows...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].UpdatedAt.Equal(ordered[j].UpdatedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].UpdatedAt.After(ordered[j].UpdatedAt)
	})
	out := []d.DefenseMemory{}
	excluded := 0
	for _, m := range ordered {
		if m.Kind != "DEFENSE" || m.Episode == nil || m.Episode.Topic != topic {
			continue
		}
		if m.Disputed || !m.ExpiresAt.After(now) || m.Episode.EvidenceVersion != version {
			excluded++
			continue
		}
		next := d.DefenseMemory{ID: m.ID, Version: m.Version, Note: m.Content, Disputed: m.Disputed, ExpiresAt: m.ExpiresAt, Episode: m.Episode}
		if len(out) >= 3 || len(d.JSON(append(append([]d.DefenseMemory{}, out...), next))) > 24576 {
			excluded++
			continue
		}
		out = append(out, next)
	}
	return out, excluded
}

func defenseMemoryInput(ctx context.Context, q Queryer, user string, a SaveDefenseMemoryArgs) (MemoryInput, error) {
	if err := a.Validate(); err != nil {
		return MemoryInput{}, err
	}
	var episode *d.DefenseEpisode
	if a.ID != "" {
		old, err := One[AgentMemory](ctx, q, "SELECT body FROM agent_memories WHERE id=? AND user_id=? AND deleted=FALSE AND expires_at>UTC_TIMESTAMP(6)", a.ID, user)
		if err != nil {
			return MemoryInput{}, err
		}
		if old.Kind != "DEFENSE" || old.Episode == nil || old.Version != a.Version {
			return MemoryInput{}, ErrConflict
		}
		episode = old.Episode
		if episode.ExecutionID != a.ExecutionID || episode.Revision != a.Revision || episode.EvidenceVersion != a.EvidenceVersion || d.Hash(d.JSON(episode.Review)) != a.ReviewHash {
			return MemoryInput{}, ErrConflict
		}
	} else {
		v, err := One[AgentExecution](ctx, q, "SELECT body FROM agent_executions WHERE id=? AND user_id=?", a.ExecutionID, user)
		if err != nil {
			return MemoryInput{}, err
		}
		s := v.Preparation
		if v.State != "COMPLETED" || s == nil || s.Engine != "langgraph" || s.Defense == nil || s.Defense.Review == nil || s.Revision != a.Revision || d.Hash(d.JSON(s.Defense.Review)) != a.ReviewHash {
			return MemoryInput{}, ErrConflict
		}
		if s.Defense.Version != a.EvidenceVersion || a.EvidenceVersion != projectdefense.Version() {
			return MemoryInput{}, ErrStaleInput
		}
		identity, _, err := preparationAgentIdentityQuery(ctx, q, user, s.Input.ApplicationID, s.Input.OtherReviews)
		if err != nil {
			return MemoryInput{}, err
		}
		if identity != v.SourceIdentity {
			return MemoryInput{}, ErrStaleInput
		}
		// Snapshot only sources actually cited by this completed review/plan.
		ids := map[string]bool{}
		for _, f := range s.Defense.Review.Findings {
			for _, r := range f.References {
				ids[r.SourceID] = true
			}
		}
		for _, i := range s.Defense.Review.Plan.Items {
			for _, r := range i.References {
				ids[r.SourceID] = true
			}
		}
		sources := []d.PreparationSource{}
		for _, src := range s.Sources {
			if ids[src.ID] {
				sources = append(sources, src)
				delete(ids, src.ID)
			}
		}
		if len(ids) > 0 {
			return MemoryInput{}, ErrConflict
		}
		episode = &d.DefenseEpisode{ExecutionID: v.ID, ApplicationID: s.Input.ApplicationID, Revision: s.Revision, Topic: s.Defense.Topic, Title: s.Defense.Title, EvidenceVersion: s.Defense.Version, PromptVersion: s.Defense.PromptVersion, RecordedAt: v.UpdatedAt, Question: s.Defense.Question, Answer: s.Defense.Answer, Review: *s.Defense.Review, Sources: sources}
	}
	note := strings.TrimSpace(a.Note)
	if note == "" {
		note = "保留这次模拟回答与反馈，供下次核对；不表示已经掌握。"
	}
	in := MemoryInput{ID: a.ID, Version: a.Version, Kind: "DEFENSE", Content: note, Scope: episode.Topic, Days: a.Days, Episode: episode, Disputed: a.Disputed}
	return in, in.Validate()
}

func (s *Store) PreviewDefenseMemory(ctx context.Context, user string, a SaveDefenseMemoryArgs) (MemoryInput, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return MemoryInput{}, err
	}
	defer tx.Rollback()
	in, err := defenseMemoryInput(ctx, tx, user, a)
	if err != nil {
		return in, err
	}
	return in, tx.Commit()
}

func saveDefenseMemoryTx(ctx context.Context, tx *sql.Tx, user string, a SaveDefenseMemoryArgs) (AgentMemory, error) {
	if a.ID == "" {
		// Converge repeated previews of the same exercise as well as nonce replay.
		old, err := One[AgentMemory](ctx, tx, `SELECT body FROM agent_memories WHERE user_id=? AND deleted=FALSE AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.kind'))='DEFENSE' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.episode.execution_id'))=? LIMIT 1`, user, a.ExecutionID)
		if err == nil {
			note := strings.TrimSpace(a.Note)
			if note == "" {
				note = "保留这次模拟回答与反馈，供下次核对；不表示已经掌握。"
			}
			if old.Episode != nil && old.Episode.Revision == a.Revision && old.Episode.EvidenceVersion == a.EvidenceVersion && d.Hash(d.JSON(old.Episode.Review)) == a.ReviewHash && old.Content == note && old.Disputed == a.Disputed && old.Days == a.Days && old.ExpiresAt.After(time.Now()) {
				return old, nil
			}
			return AgentMemory{}, ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return AgentMemory{}, err
		}
	}
	in, err := defenseMemoryInput(ctx, tx, user, a)
	if err != nil {
		return AgentMemory{}, err
	}
	return saveMemoryTx(ctx, tx, user, in)
}
