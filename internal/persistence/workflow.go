package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/resume"
	"time"
)

type CreateArgs struct {
	JobID     string `json:"job_id"`
	Resume    string `json:"resume_version,omitempty"`
	Submitted bool   `json:"submitted,omitempty"`
}
type TransitionArgs struct {
	ApplicationID string `json:"application_id"`
	State         string `json:"state"`
	Version       int    `json:"version"`
	Note          string `json:"note,omitempty"`
}

func ValidateAction(kind string, raw []byte) error {
	switch kind {
	case "remember_memory":
		var a MemoryInput
		if err := d.Strict(raw, &a); err != nil {
			return err
		}
		return a.Validate()
	case "watch_source":
		var a d.WatchInput
		if err := d.Strict(raw, &a); err != nil {
			return err
		}
		return a.Validate()
	case "unwatch_source":
		var a struct {
			WatchID string `json:"watch_id"`
		}
		if err := d.Strict(raw, &a); err != nil {
			return err
		}
		if len(a.WatchID) != 32 {
			return ErrValidation
		}

	case "create_application", "record_submitted_application":
		var a CreateArgs
		if err := d.Strict(raw, &a); err != nil {
			return err
		}
		if len(a.JobID) != 32 || len(a.Resume) > 200 || (kind == "create_application" && a.Submitted) {
			return ErrValidation
		}
	case "transition_application":
		var a TransitionArgs
		if err := d.Strict(raw, &a); err != nil {
			return err
		}
		if len(a.ApplicationID) != 32 || a.Version < 1 || len(a.Note) > 2000 {
			return ErrValidation
		}
		valid := false
		for _, s := range []string{"PLANNED", "APPLIED", "OA", "INTERVIEW", "HR", "OFFER", "REJECTED", "WITHDRAWN"} {
			if s == a.State {
				valid = true
			}
		}
		if !valid {
			return ErrValidation
		}
	case "record_interview_review":
		var a d.Review
		if err := d.Strict(raw, &a); err != nil {
			return err
		}
		return a.Validate()
	default:
		return ErrValidation
	}
	return nil
}
func (s *Store) ApplyAction(ctx context.Context, user, nonce, kind string, raw []byte) (json.RawMessage, error) {
	if err := ValidateAction(kind, raw); err != nil {
		return nil, err
	}
	var result json.RawMessage
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		// Serialize a user's writes, including confirmation receipts and weak-topic accumulation.
		var locked string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&locked); err != nil {
			return err
		}
		var receiptUser string
		var old []byte
		err := tx.QueryRowContext(ctx, "SELECT user_id,result FROM action_receipts WHERE id=?", nonce).Scan(&receiptUser, &old)
		if err == nil {
			if receiptUser != user {
				return ErrNotFound
			}
			result = old
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := time.Now().UTC()
		switch kind {
		case "remember_memory":
			var a MemoryInput
			json.Unmarshal(raw, &a)
			v, e := saveMemoryTx(ctx, tx, user, a)
			err = e
			result = []byte(d.JSON(v))
		case "watch_source":
			var a d.WatchInput
			json.Unmarshal(raw, &a)
			v, e := createWatchTx(ctx, tx, user, a)
			err = e
			result = []byte(d.JSON(v))
		case "unwatch_source":
			var a struct {
				WatchID string `json:"watch_id"`
			}
			json.Unmarshal(raw, &a)
			err = deleteWatchTx(ctx, tx, user, a.WatchID)
			result = []byte(d.JSON(map[string]bool{"deleted": err == nil}))

		case "create_application", "record_submitted_application":
			var a CreateArgs
			json.Unmarshal(raw, &a)
			if err := CheckJobAccess(ctx, tx, user, a.JobID); err != nil {
				return err
			}
			if kind == "create_application" {
				if err := checkCampaignPlan(ctx, tx, user, a.JobID); err != nil {
					return err
				}
			}
			v := d.Application{ID: d.ID(), UserID: user, JobID: a.JobID, State: "PLANNED", Version: 1, ResumeVersion: a.Resume, CreatedAt: now, UpdatedAt: now}
			if kind == "record_submitted_application" {
				v.State = "APPLIED"
				v.AppliedAt = &now
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO applications(id,user_id,job_id,version,body) VALUES(?,?,?,?,?)", v.ID, user, v.JobID, 1, d.JSON(v))
			if duplicate(err) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			event := d.ApplicationEvent{ID: d.ID(), ApplicationID: v.ID, To: v.State, OccurredAt: now, Actor: user}
			_, err = tx.ExecContext(ctx, "INSERT INTO application_events(id,application_id,body) VALUES(?,?,?)", event.ID, v.ID, d.JSON(event))
			result = []byte(d.JSON(v))
		case "transition_application":
			var a TransitionArgs
			json.Unmarshal(raw, &a)
			v, e := One[d.Application](ctx, tx, "SELECT body FROM applications WHERE id=? AND user_id=? FOR UPDATE", a.ApplicationID, user)
			if e != nil {
				return e
			}
			if v.Version != a.Version {
				return ErrConflict
			}
			if e = d.Transition(v.State, a.State); e != nil {
				return errors.Join(ErrValidation, e)
			}
			event := d.ApplicationEvent{ID: d.ID(), ApplicationID: v.ID, From: v.State, To: a.State, OccurredAt: now, Note: a.Note, Actor: user}
			_, err = tx.ExecContext(ctx, "INSERT INTO application_events(id,application_id,body) VALUES(?,?,?)", event.ID, v.ID, d.JSON(event))
			if err != nil {
				return err
			}
			v.State = a.State
			v.Version++
			v.UpdatedAt = now
			if a.State == "APPLIED" {
				v.AppliedAt = &now
			}
			res, e := tx.ExecContext(ctx, "UPDATE applications SET version=?,body=? WHERE id=? AND user_id=? AND version=?", v.Version, d.JSON(v), v.ID, user, a.Version)
			if e != nil {
				return e
			}
			n, e := res.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return ErrConflict
			}
			result = []byte(d.JSON(v))
		case "record_interview_review":
			var r d.Review
			json.Unmarshal(raw, &r)
			var found string
			if e := tx.QueryRowContext(ctx, "SELECT id FROM interviews WHERE id=? AND user_id=?", r.InterviewID, user).Scan(&found); e != nil {
				return e
			}
			r.ID = d.ID()
			_, err = tx.ExecContext(ctx, "INSERT INTO reviews(id,user_id,interview_id,body) VALUES(?,?,?,?)", r.ID, user, r.InterviewID, d.JSON(r))
			if duplicate(err) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, c := range r.Topics {
				topic := d.Normalize(c.Topic)
				if seen[topic] {
					continue
				}
				seen[topic] = true
				w, e := One[d.WeakTopic](ctx, tx, "SELECT body FROM weak_topics WHERE user_id=? AND topic=? FOR UPDATE", user, topic)
				if errors.Is(e, sql.ErrNoRows) {
					w = d.WeakTopic{Topic: topic, FirstSeen: now, Evidence: []string{}}
				} else if e != nil {
					return e
				}
				w.Count++
				if c.Weight > w.Weight {
					w.Weight = c.Weight
				}
				w.LastSeen = now
				w.Evidence = append(w.Evidence, r.ID+":"+c.Evidence)
				_, err = tx.ExecContext(ctx, "INSERT INTO weak_topics(id,user_id,topic,body) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE body=VALUES(body)", d.ID(), user, topic, d.JSON(w))
				if err != nil {
					return err
				}
			}
			result = []byte(d.JSON(r))
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO action_receipts(id,user_id,result,created_at) VALUES(?,?,?,?)", nonce, user, string(result), now)
		return err
	})
	return result, err
}
func (s *Store) ApplicationHistory(ctx context.Context, user, id string) ([]d.ApplicationEvent, error) {
	return Many[d.ApplicationEvent](ctx, s.DB, "SELECT e.body FROM application_events e JOIN applications a ON a.id=e.application_id WHERE a.user_id=? AND a.id=? ORDER BY JSON_UNQUOTE(JSON_EXTRACT(e.body,'$.occurred_at')),e.id", user, id)
}
func (s *Store) SaveInterview(ctx context.Context, user string, v d.Interview) (d.Interview, error) {
	if v.ApplicationID == "" || v.Round < 1 || v.Round > 20 || v.ScheduledAt.IsZero() || len(v.Notes) > 8000 {
		return v, ErrValidation
	}
	if v.Result == "" {
		v.Result = "PENDING"
	}
	if v.Result != "PASS" && v.Result != "FAIL" && v.Result != "PENDING" {
		return v, ErrValidation
	}
	v.ID = d.ID()
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM applications WHERE id=? AND user_id=?", v.ApplicationID, user).Scan(&id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO interviews(id,user_id,application_id,body) VALUES(?,?,?,?)", v.ID, user, v.ApplicationID, d.JSON(v))
		return err
	})
	return v, err
}
func (s *Store) FinishInterview(ctx context.Context, user, id, result, notes string) (d.Interview, error) {
	var v d.Interview
	if (result != "PASS" && result != "FAIL" && result != "PENDING") || len(notes) > 8000 {
		return v, ErrValidation
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		v, err = One[d.Interview](ctx, tx, "SELECT body FROM interviews WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		if v.FinishedAt == nil {
			now := time.Now().UTC()
			v.FinishedAt = &now
		}
		v.Result = result
		v.Notes = notes
		_, err = tx.ExecContext(ctx, "UPDATE interviews SET body=? WHERE id=? AND user_id=?", d.JSON(v), id, user)
		return err
	})
	return v, err
}
func (s *Store) SaveProject(ctx context.Context, user string, p d.Project) (d.Project, error) {
	if !validProject(p) {
		return p, ErrValidation
	}
	if p.Bullets == nil {
		p.Bullets = []string{}
	}
	p.ID = d.ID()
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO projects(id,user_id,body) VALUES(?,?,?)", p.ID, user, d.JSON(p))
		if e == nil {
			e = agentEvent(ctx, tx, user, "", "PROFILE_CHANGED")
		}
		return e
	})
	return p, err
}
func (s *Store) UpdateProject(ctx context.Context, user, id string, incoming d.Project) (d.Project, error) {
	if id == "" || !validProject(incoming) {
		return d.Project{}, ErrValidation
	}
	var updated d.Project
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		old, err := One[d.Project](ctx, tx, "SELECT body FROM projects WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		updated = incoming
		updated.ID = old.ID
		// Older clients only rename projects. Missing content must not erase it;
		// a supplied empty bullets array explicitly clears the content.
		if incoming.Bullets == nil && incoming.Description == "" {
			updated.Description, updated.Bullets = old.Description, old.Bullets
		}
		_, err = tx.ExecContext(ctx, "UPDATE projects SET body=? WHERE id=? AND user_id=?", d.JSON(updated), id, user)
		if err == nil {
			err = agentEvent(ctx, tx, user, "", "PROFILE_CHANGED")
		}
		return err
	})
	return updated, err
}

func validProject(p d.Project) bool {
	if p.Validate() != nil || resume.HasSensitive(p.Name) || resume.HasSensitive(p.Description) {
		return false
	}
	for _, bullet := range p.Bullets {
		if resume.HasSensitive(bullet) {
			return false
		}
	}
	return true
}

// DeleteProject removes the project and its facts in one transaction. The user
// lock is shared with fact writes, so a concurrent save cannot leave orphaned facts.
func (s *Store) DeleteProject(ctx context.Context, user, id string) (int64, error) {
	if len(id) != 32 {
		return 0, ErrValidation
	}
	var deleted int64
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var locked string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&locked); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT id FROM projects WHERE id=? AND user_id=? FOR UPDATE", id, user).Scan(&locked); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM project_facts WHERE project_id=? AND user_id=?", id, user)
		if err != nil {
			return err
		}
		deleted, err = result.RowsAffected()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM projects WHERE id=? AND user_id=?", id, user)
		if err == nil {
			err = agentEvent(ctx, tx, user, "", "PROFILE_CHANGED")
		}
		return err
	})
	if err != nil {
		return 0, err
	}
	return deleted, nil
}

func (s *Store) DeleteFact(ctx context.Context, user, id string) error {
	if len(id) != 32 {
		return ErrValidation
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var locked string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&locked); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM project_facts WHERE id=? AND user_id=?", id, user)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrNotFound
		}
		return agentEvent(ctx, tx, user, "", "PROFILE_CHANGED")
	})
}

func (s *Store) SaveFact(ctx context.Context, user string, f d.ProjectFact) (d.ProjectFact, error) {
	if err := f.Validate(); err != nil {
		return f, ErrValidation
	}
	if len(f.Reference) > 2000 || resume.HasSensitive(f.Claim) || resume.HasSensitive(f.Reference) {
		return f, ErrValidation
	}
	f.ID = d.ID()
	f.CreatedAt = time.Now().UTC()
	f.UpdatedAt = f.CreatedAt
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var id string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&id); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT id FROM projects WHERE id=? AND user_id=?", f.ProjectID, user).Scan(&id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO project_facts(id,user_id,project_id,body) VALUES(?,?,?,?)", f.ID, user, f.ProjectID, d.JSON(f))
		if err == nil {
			err = agentEvent(ctx, tx, user, "", "PROFILE_CHANGED")
		}
		return err
	})
	return f, err
}

func (s *Store) UpdateFact(ctx context.Context, user, id string, incoming d.ProjectFact) (d.ProjectFact, error) {
	var updated d.ProjectFact
	if id == "" || len(incoming.Reference) > 2000 || resume.HasSensitive(incoming.Claim) || resume.HasSensitive(incoming.Reference) {
		return updated, ErrValidation
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
			return err
		}
		old, err := One[d.ProjectFact](ctx, tx, "SELECT body FROM project_facts WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		if incoming.ProjectID != old.ProjectID {
			return ErrValidation
		}
		if err := incoming.Validate(); err != nil {
			return ErrValidation
		}
		updated = incoming
		updated.ID = old.ID
		updated.CreatedAt = old.CreatedAt
		updated.UpdatedAt = time.Now().UTC()
		_, err = tx.ExecContext(ctx, "UPDATE project_facts SET body=? WHERE id=? AND user_id=?", d.JSON(updated), id, user)
		if err == nil {
			err = agentEvent(ctx, tx, user, "", "PROFILE_CHANGED")
		}
		return err
	})
	return updated, err
}

func (s *Store) ActionReceipt(ctx context.Context, user, id string) (json.RawMessage, error) {
	var result json.RawMessage
	err := s.DB.QueryRowContext(ctx, "SELECT result FROM action_receipts WHERE id=? AND user_id=?", id, user).Scan(&result)
	return result, err
}
