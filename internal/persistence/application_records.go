package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type ApplicationJob struct {
	ID            string   `json:"id"`
	Company       string   `json:"company"`
	Title         string   `json:"title"`
	Locations     []string `json:"locations"`
	JobType       string   `json:"job_type"`
	CurrentStatus string   `json:"current_status"`
}

type ApplicationRecord struct {
	d.Application
	Job         *ApplicationJob `json:"job,omitempty"`
	OfficialURL string          `json:"official_url,omitempty"`
	NextStates  []string        `json:"next_states"`
}

// Join only visible jobs and official sources. Application ownership never
// permits reading another user's private job/source metadata.
func (s *Store) ApplicationRecords(ctx context.Context, user string) ([]ApplicationRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT a.body,j.body,
 (SELECT JSON_UNQUOTE(JSON_EXTRACT(p.body,'$.url')) FROM postings p JOIN sources src ON src.id=p.source_id
  WHERE p.job_id=j.id AND JSON_UNQUOTE(JSON_EXTRACT(src.body,'$.trust'))='OFFICIAL'
  AND (JSON_UNQUOTE(JSON_EXTRACT(src.body,'$.visibility'))='GLOBAL' OR
       (JSON_UNQUOTE(JSON_EXTRACT(src.body,'$.visibility'))='PRIVATE' AND JSON_UNQUOTE(JSON_EXTRACT(src.body,'$.owner_id'))=?))
  AND JSON_UNQUOTE(JSON_EXTRACT(p.body,'$.url'))<>'' ORDER BY JSON_UNQUOTE(JSON_EXTRACT(p.body,'$.last_seen_at')) DESC,p.id LIMIT 1)
 FROM applications a LEFT JOIN jobs j ON j.id=a.job_id AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))
 WHERE a.user_id=? ORDER BY JSON_UNQUOTE(JSON_EXTRACT(a.body,'$.updated_at')) DESC,a.id LIMIT 500`, user, user, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ApplicationRecord{}
	for rows.Next() {
		var body, job []byte
		var official sql.NullString
		if err := rows.Scan(&body, &job, &official); err != nil {
			return nil, err
		}
		v := ApplicationRecord{NextStates: []string{}}
		if err := json.Unmarshal(body, &v.Application); err != nil {
			return nil, err
		}
		if len(job) > 0 {
			v.Job = new(ApplicationJob)
			if err := json.Unmarshal(job, v.Job); err != nil {
				return nil, err
			}
		}
		if u, err := url.Parse(official.String); err == nil && len(official.String) <= 2048 && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil {
			v.OfficialURL = official.String
		}
		for _, state := range []string{"APPLIED", "OA", "INTERVIEW", "HR", "OFFER", "REJECTED", "WITHDRAWN"} {
			if d.Transition(v.State, state) == nil {
				v.NextStates = append(v.NextStates, state)
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type ApplicationDetails struct {
	Version       int    `json:"version"`
	ResumeVersion string `json:"resume_version"`
	Note          string `json:"note"`
}

// Metadata edits share the workflow's version guard, so concurrent state or
// details updates cannot silently overwrite one another. State/time stay intact.
func (s *Store) UpdateApplicationDetails(ctx context.Context, user, id string, in ApplicationDetails) (d.Application, error) {
	var v d.Application
	in.ResumeVersion, in.Note = strings.TrimSpace(in.ResumeVersion), strings.TrimSpace(in.Note)
	if len(id) != 32 || in.Version < 1 || len(in.ResumeVersion) > 200 || len(in.Note) > 2000 {
		return v, ErrValidation
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var locked string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&locked); err != nil {
			return err
		}
		old, err := One[d.Application](ctx, tx, "SELECT body FROM applications WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		if old.Version != in.Version {
			return ErrConflict
		}
		v = old
		v.ResumeVersion, v.Note = in.ResumeVersion, in.Note
		v.Version++
		v.UpdatedAt = time.Now().UTC()
		if _, err := tx.ExecContext(ctx, "UPDATE applications SET version=?,body=? WHERE id=? AND user_id=?", v.Version, d.JSON(v), id, user); err != nil {
			return err
		}
		event := d.ApplicationEvent{ID: d.ID(), ApplicationID: id, From: v.State, To: v.State, OccurredAt: v.UpdatedAt, Actor: user, Note: "更新投递资料；简历版本：" + v.ResumeVersion}
		_, err = tx.ExecContext(ctx, "INSERT INTO application_events(id,application_id,body) VALUES(?,?,?)", event.ID, id, d.JSON(event))
		return err
	})
	return v, err
}
