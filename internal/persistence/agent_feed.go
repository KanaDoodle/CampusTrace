package persistence

import (
	"context"
	"database/sql"
	"errors"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type AgentTodo struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	JobID       string    `json:"job_id,omitempty"`
	Company     string    `json:"company,omitempty"`
	Title       string    `json:"title"`
	Explanation string    `json:"explanation"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
}
type AgentFeed struct {
	Enabled bool        `json:"enabled"`
	Items   []AgentTodo `json:"items"`
	More    bool        `json:"more"`
}

func agentEvent(ctx context.Context, tx *sql.Tx, owner, job, kind string) error {
	_, e := tx.ExecContext(ctx, "INSERT INTO agent_events(owner_id,job_id,kind,created_at) VALUES(?,?,?,?)", owner, job, kind, time.Now().UTC())
	return e
}
func (s *Store) SetAgentFeed(ctx context.Context, user string, enabled bool) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		var cursor uint64
		if e := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM agent_events").Scan(&cursor); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, "INSERT INTO agent_feed_settings(user_id,enabled,event_cursor) VALUES(?,?,?) ON DUPLICATE KEY UPDATE enabled=VALUES(enabled)", user, enabled, cursor)
		return e
	})
}
func (s *Store) AgentTodos(ctx context.Context, user string) (AgentFeed, error) {
	var out AgentFeed
	out.Items = []AgentTodo{}
	var enabled bool
	e := s.DB.QueryRowContext(ctx, "SELECT enabled FROM agent_feed_settings WHERE user_id=?", user).Scan(&enabled)
	if e == sql.ErrNoRows {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	out.Enabled = enabled
	out.Items, e = Many[AgentTodo](ctx, s.DB, "SELECT body FROM agent_todos WHERE user_id=? AND state='OPEN' ORDER BY created_at DESC,id LIMIT 51", user)
	if len(out.Items) > 50 {
		out.Items = out.Items[:50]
		out.More = true
	}
	return out, e
}
func (s *Store) DismissAgentTodo(ctx context.Context, user, id string) error {
	r, e := s.DB.ExecContext(ctx, "UPDATE agent_todos SET state='DONE',body=JSON_SET(body,'$.state','DONE') WHERE id=? AND user_id=? AND state='OPEN'", id, user)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) RefreshAgentFeed(ctx context.Context, user string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		var enabled bool
		var cursor uint64
		if e := tx.QueryRowContext(ctx, "SELECT enabled,event_cursor FROM agent_feed_settings WHERE user_id=? FOR UPDATE", user).Scan(&enabled, &cursor); e == sql.ErrNoRows {
			return nil
		} else if e != nil {
			return e
		}
		if !enabled {
			return nil
		}
		rows, e := tx.QueryContext(ctx, "SELECT id,owner_id,job_id,kind,created_at FROM agent_events WHERE id>? ORDER BY id LIMIT 200", cursor)
		if e != nil {
			return e
		}
		type event struct {
			id               uint64
			owner, job, kind string
			at               time.Time
		}
		events := []event{}
		for rows.Next() {
			var v event
			if e = rows.Scan(&v.id, &v.owner, &v.job, &v.kind, &v.at); e != nil {
				rows.Close()
				return e
			}
			events = append(events, v)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, v := range events {
			cursor = v.id
			if v.owner != "" && v.owner != user {
				continue
			}
			t := AgentTodo{ID: d.ID(), Kind: v.kind, JobID: v.job, State: "OPEN", CreatedAt: v.at}
			if v.job != "" {
				if e = CheckJobAccess(ctx, tx, user, v.job); e != nil {
					if errors.Is(e, ErrNotFound) {
						continue
					}
					return e
				}
				j, e := One[d.Job](ctx, tx, "SELECT body FROM jobs WHERE id=?", v.job)
				if e != nil {
					return e
				}
				t.Company = j.Company
				t.Title = j.Title
				var ignored int
				if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_job_preferences WHERE user_id=? AND job_id=? AND disposition='IGNORED'", user, v.job).Scan(&ignored); e != nil {
					return e
				}
				if ignored > 0 {
					continue
				}
			}
			switch v.kind {
			case "NEW_JOB":
				t.Explanation = "招聘源新增岗位，可先按主投方向筛选，再决定是否分析。"
			case "JD_CHANGED":
				t.Explanation = "岗位原文发生变化，请核对已有分析是否仍适用。"
			case "PROFILE_CHANGED":
				t.Title = "求职资料已更新"
				t.Explanation = "已核对资料发生变化，可检查旧分析并选择需要更新的岗位。"
			default:
				continue
			}
			// Coalesce an open item for the same job/kind; different events still
			// advance the durable cursor and never trigger model requests.
			var n int
			if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM agent_todos WHERE user_id=? AND state='OPEN' AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.kind'))=? AND COALESCE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.job_id')),'')=?", user, v.kind, v.job).Scan(&n); e != nil {
				return e
			}
			if n > 0 {
				continue
			}
			_, e = tx.ExecContext(ctx, "INSERT IGNORE INTO agent_todos(id,user_id,event_id,state,created_at,body) VALUES(?,?,?,'OPEN',?,?)", t.ID, user, v.id, v.at, d.JSON(t))
			if e != nil {
				return e
			}
		}
		_, e = tx.ExecContext(ctx, "UPDATE agent_feed_settings SET event_cursor=? WHERE user_id=?", cursor, user)
		return e
	})
}
func (s *Store) RefreshEnabledAgentFeeds(ctx context.Context) error {
	rows, e := s.DB.QueryContext(ctx, "SELECT user_id FROM agent_feed_settings WHERE enabled=TRUE ORDER BY user_id LIMIT 1000")
	if e != nil {
		return e
	}
	users := []string{}
	for rows.Next() {
		var u string
		if e = rows.Scan(&u); e != nil {
			rows.Close()
			return e
		}
		users = append(users, u)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, u := range users {
		if e = s.RefreshAgentFeed(ctx, u); e != nil {
			return e
		}
	}
	return nil
}
