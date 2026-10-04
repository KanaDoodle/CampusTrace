package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
)

type CampusRegistration struct {
	Source   d.Source      `json:"source"`
	Watch    d.WatchTarget `json:"watch"`
	Existing bool          `json:"existing"`
}

// CreateCampusWatch keeps a user-entered recruitment site private and creates
// its schedule in the same transaction. The caller has already resolved the
// supported public URL to a project code through the source adapter.
func (s *Store) CreateCampusWatch(ctx context.Context, user, projectCode, projectName string, input d.WatchInput) (CampusRegistration, error) {
	if projectName == "" {
		return CampusRegistration{}, ErrValidation
	}
	return s.CreateCampusSource(ctx, user, "xiaohongshu", projectCode, "小红书 · "+projectName, input)
}

func (s *Store) CreateCampusSource(ctx context.Context, user, adapter, projectCode, name string, input d.WatchInput) (CampusRegistration, error) {
	var out CampusRegistration
	validScope := adapter == "xiaohongshu" || (adapter == "baidu" && projectCode == "GRADUATE") || (adapter == "meituan" && projectCode == "graduate") || (adapter == "jd" && projectCode == "present") || (adapter == "netease" && projectCode == "103") || (adapter == "alibaba" && projectCode == "100000760001")
	if user == "" || projectCode == "" || len(projectCode) > 100 || len(name) == 0 || len(name) > 160 || strings.ContainsAny(projectCode, "/:?@#") || !validScope {
		return out, ErrValidation
	}
	checked := input
	checked.SourceID = "pending"
	if checked.Validate() != nil || input.CheckInterval < d.SourceMinimumInterval(adapter) || (adapter != "xiaohongshu" && input.Direction != "") {
		return out, ErrValidation
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=? FOR UPDATE", user).Scan(&owner); err != nil {
			return err
		}
		src, err := One[d.Source](ctx, tx, "SELECT body FROM sources WHERE JSON_UNQUOTE(JSON_EXTRACT(body,'$.owner_id'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.adapter'))=? AND JSON_UNQUOTE(JSON_EXTRACT(body,'$.tenant'))=? LIMIT 1", user, adapter, projectCode)
		if errors.Is(err, sql.ErrNoRows) {
			src = d.Source{ID: d.ID(), Name: name, Adapter: adapter, Tenant: projectCode, RateLimit: 30, OwnerID: user, Visibility: "PRIVATE", Type: "MANUAL", Trust: "MANUAL", Timezone: "Asia/Shanghai"}
			if _, err = tx.ExecContext(ctx, "INSERT INTO sources(id,body) VALUES(?,?)", src.ID, d.JSON(src)); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		out.Source = src
		watch, err := One[d.WatchTarget](ctx, tx, "SELECT body FROM watch_targets WHERE user_id=? AND source_id=? ORDER BY id LIMIT 1", user, src.ID)
		if err == nil {
			out.Watch = watch
			out.Existing = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		input.SourceID = src.ID
		out.Watch, err = createWatchTx(ctx, tx, user, input)
		return err
	})
	return out, err
}

type WatchProgress struct {
	Expected  int `json:"expected"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

func (s *Store) ProgressForUser(ctx context.Context, user, id string) (WatchProgress, error) {
	var out WatchProgress
	watch, err := s.Watch(ctx, user, id)
	if err != nil {
		return out, err
	}
	err = s.DB.QueryRowContext(ctx, "SELECT expected_count,completed_count,failed_count FROM watch_runs WHERE watch_id=? AND schedule_version=?", id, watch.ScheduleVersion).Scan(&out.Expected, &out.Completed, &out.Failed)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	return out, err
}

type SourceJobsPage struct {
	Jobs     []d.Job `json:"jobs"`
	Total    int     `json:"total"`
	Page     int     `json:"page"`
	PageSize int     `json:"page_size"`
}

func (s *Store) SourceJobsForUser(ctx context.Context, user, sourceID string, page int) (SourceJobsPage, error) {
	out := SourceJobsPage{Jobs: []d.Job{}, Page: page, PageSize: 50}
	if page < 1 || page > 10 {
		return out, ErrValidation
	}
	if _, err := watchSource(ctx, s.DB, user, sourceID); err != nil {
		return out, err
	}
	err := s.DB.QueryRowContext(ctx, "SELECT COUNT(DISTINCT p.job_id) FROM postings p JOIN jobs j ON j.id=p.job_id WHERE p.source_id=? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))", sourceID, user).Scan(&out.Total)
	if err != nil {
		return out, err
	}
	out.Jobs, err = Many[d.Job](ctx, s.DB, "SELECT j.body FROM postings p JOIN jobs j ON j.id=p.job_id WHERE p.source_id=? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?)) GROUP BY j.id ORDER BY j.id LIMIT 50 OFFSET ?", sourceID, user, (page-1)*50)
	return out, err
}
