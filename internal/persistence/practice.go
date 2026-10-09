package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/practice"
	"time"
)

type PracticeRun struct {
	Code        string    `json:"code,omitempty"`
	Tests       string    `json:"tests,omitempty"`
	ID          string    `json:"id"`
	RequestKey  string    `json:"request_key"`
	Fingerprint string    `json:"-"`
	CreatedAt   time.Time `json:"created_at"`
	practice.Result
}

func (s *Store) PracticeRuns(ctx context.Context, user string) ([]PracticeRun, error) {
	// A crashed caller never silently re-executes a persisted RUNNING receipt.
	_, err := s.DB.ExecContext(ctx, `UPDATE practice_runs SET state='INTERRUPTED',body=JSON_SET(body,'$.state','INTERRUPTED','$.output','上次运行未返回完整结果；需要再次运行时，请发起新的练习。') WHERE user_id=? AND state='RUNNING' AND created_at<UTC_TIMESTAMP(6)-INTERVAL 60 SECOND`, user)
	if err != nil {
		return nil, err
	}
	return Many[PracticeRun](ctx, s.DB, "SELECT body FROM practice_runs WHERE user_id=? ORDER BY created_at DESC,id LIMIT 20", user)
}
func (s *Store) BeginPractice(ctx context.Context, user string, in practice.Input) (out PracticeRun, fresh bool, err error) {
	if len(in.RequestKey) < 8 || len(in.RequestKey) > 64 || practice.Validate(in.Code, in.Tests) != nil {
		return out, false, ErrValidation
	}
	fingerprint := d.Hash(practice.Policy + "\n" + in.Code + "\n" + in.Tests)
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		old, e := One[PracticeRun](ctx, tx, "SELECT body FROM practice_runs WHERE user_id=? AND request_key=? FOR UPDATE", user, in.RequestKey)
		if e == nil {
			var fp string
			if e = tx.QueryRowContext(ctx, "SELECT fingerprint FROM practice_runs WHERE id=?", old.ID).Scan(&fp); e != nil {
				return e
			}
			if fp != fingerprint {
				return ErrConflict
			}
			out = old
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		var active int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM practice_runs WHERE user_id=? AND state='RUNNING' AND created_at>UTC_TIMESTAMP(6)-INTERVAL 60 SECOND", user).Scan(&active); e != nil {
			return e
		}
		if active > 0 {
			return ErrConflict
		}
		out = PracticeRun{ID: d.ID(), Code: in.Code, Tests: in.Tests, RequestKey: in.RequestKey, Fingerprint: fingerprint, CreatedAt: time.Now().UTC(), Result: practice.Result{State: "RUNNING", ExitCode: -1, Policy: practice.Policy}}
		_, e = tx.ExecContext(ctx, "INSERT INTO practice_runs(id,user_id,request_key,fingerprint,state,created_at,body) VALUES(?,?,?,?,?,?,?)", out.ID, user, out.RequestKey, fingerprint, out.State, out.CreatedAt, d.JSON(out))
		fresh = e == nil
		return e
	})
	return
}
func (s *Store) FinishPractice(ctx context.Context, user string, v PracticeRun) error {
	switch v.State {
	case "PASSED", "FAILED", "TIMEOUT", "OOM", "INTERRUPTED", "ERROR":
	default:
		return ErrValidation
	}
	if len(v.Output) > practice.MaxOutput {
		return ErrValidation
	}
	_, e := s.DB.ExecContext(ctx, "UPDATE practice_runs SET state=?,body=? WHERE id=? AND user_id=? AND state='RUNNING'", v.State, d.JSON(v), v.ID, user)
	return e
}

func (s *Store) PracticeReceipt(ctx context.Context, user string, in practice.Input) (PracticeRun, error) {
	if len(in.RequestKey) < 8 || len(in.RequestKey) > 64 {
		return PracticeRun{}, ErrValidation
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT body,fingerprint FROM practice_runs WHERE user_id=? AND request_key=?", user, in.RequestKey)
	if err != nil {
		return PracticeRun{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if e := rows.Err(); e != nil {
			return PracticeRun{}, e
		}
		return PracticeRun{}, ErrNotFound
	}
	var b []byte
	var fingerprint string
	if err = rows.Scan(&b, &fingerprint); err != nil {
		return PracticeRun{}, err
	}
	if fingerprint != d.Hash(practice.Policy+"\n"+in.Code+"\n"+in.Tests) {
		return PracticeRun{}, ErrConflict
	}
	var out PracticeRun
	if err = json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	return out, nil
}
