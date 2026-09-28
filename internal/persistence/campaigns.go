package persistence

import (
	"context"
	"database/sql"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"strings"
	"time"
)

var ErrCampaignLimit = errors.New("application campaign quota reserved")

type ApplicationCampaign struct {
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	CompanyID string    `json:"company_id"`
	Company   string    `json:"company"`
	Name      string    `json:"name"`
	Limit     int       `json:"limit"`
	JobIDs    []string  `json:"job_ids"`
	RuleURL   string    `json:"rule_url"`
	Confirmed bool      `json:"confirmed"`
	UpdatedAt time.Time `json:"updated_at"`
}
type CampaignView struct {
	ApplicationCampaign
	Planned   int  `json:"planned"`
	Submitted int  `json:"submitted"`
	Remaining int  `json:"remaining"`
	Conflict  bool `json:"conflict"`
}

func (s *Store) CampaignCatalog(ctx context.Context, user string) ([]ApplicationJob, error) {
	return Many[ApplicationJob](ctx, s.DB, "SELECT body FROM jobs WHERE visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?) ORDER BY company_name,id LIMIT 10000", user)
}
func campaignUsage(ctx context.Context, q Queryer, user, id string) (int, int, error) {
	apps, err := Many[d.Application](ctx, q, "SELECT a.body FROM applications a JOIN application_campaign_jobs c ON c.user_id=a.user_id AND c.job_id=a.job_id WHERE c.user_id=? AND c.campaign_id=?", user, id)
	if err != nil {
		return 0, 0, err
	}
	planned, submitted := 0, 0
	for _, a := range apps {
		if a.AppliedAt != nil {
			submitted++
		} else if a.State == "PLANNED" {
			planned++
		}
	}
	return planned, submitted, nil
}
func (s *Store) Campaigns(ctx context.Context, user string) ([]CampaignView, error) {
	out := []CampaignView{}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rules, err := Many[ApplicationCampaign](ctx, tx, "SELECT body FROM application_campaigns WHERE user_id=? ORDER BY id", user)
	if err != nil {
		return out, err
	}
	for _, v := range rules {
		a, b, err := campaignUsage(ctx, tx, user, v.ID)
		if err != nil {
			return out, err
		}
		remaining := v.Limit - a - b
		if remaining < 0 {
			remaining = 0
		}
		out = append(out, CampaignView{v, a, b, remaining, a+b > v.Limit})
	}
	return out, tx.Commit()
}
func (s *Store) SaveCampaign(ctx context.Context, user, id string, in ApplicationCampaign) (ApplicationCampaign, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.RuleURL = strings.TrimSpace(in.RuleURL)
	if len(in.CompanyID) != 32 || in.Name == "" || len(in.Name) > 200 || !in.Confirmed || in.Limit < 1 || in.Limit > 10 || len(in.JobIDs) < 1 || len(in.JobIDs) > 200 || len(in.RuleURL) > 2048 {
		return in, ErrValidation
	}
	if in.RuleURL != "" {
		u, err := url.Parse(in.RuleURL)
		if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
			return in, ErrValidation
		}
	}
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		if id != "" {
			old, err := One[ApplicationCampaign](ctx, tx, "SELECT body FROM application_campaigns WHERE id=? AND user_id=? FOR UPDATE", id, user)
			if err != nil {
				return err
			}
			if old.Version != in.Version {
				return ErrConflict
			}
			in.Version++
		} else {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM application_campaigns WHERE user_id=?", user).Scan(&count); err != nil {
				return err
			}
			if count >= 50 {
				return ErrValidation
			}
			id = d.ID()
			in.Version = 1
		}
		slots := []string{}
		args := []any{user, in.CompanyID}
		seen := map[string]bool{}
		for _, job := range in.JobIDs {
			if len(job) != 32 || seen[job] {
				return ErrValidation
			}
			seen[job] = true
			slots = append(slots, "?")
			args = append(args, job)
		}
		jobs, err := Many[ApplicationJob](ctx, tx, "SELECT body FROM jobs WHERE (visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?)) AND company_id=? AND id IN ("+strings.Join(slots, ",")+")", args...)
		if err != nil {
			return err
		}
		if len(jobs) != len(in.JobIDs) {
			return ErrNotFound
		}
		in.ID = id
		in.Company = jobs[0].Company
		in.UpdatedAt = time.Now().UTC()
		_, err = tx.ExecContext(ctx, "INSERT INTO application_campaigns(id,user_id,company_id,version,body) VALUES(?,?,?,?,?) ON DUPLICATE KEY UPDATE company_id=VALUES(company_id),version=VALUES(version),body=VALUES(body)", id, user, in.CompanyID, in.Version, d.JSON(in))
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM application_campaign_jobs WHERE user_id=? AND campaign_id=?", user, id); err != nil {
			return err
		}
		for _, job := range in.JobIDs {
			_, err = tx.ExecContext(ctx, "INSERT INTO application_campaign_jobs(user_id,job_id,campaign_id) VALUES(?,?,?)", user, job, id)
			if duplicate(err) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
	return in, err
}
func (s *Store) DeleteCampaign(ctx context.Context, user, id string, version int) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if err := lockRunUser(ctx, tx, user); err != nil {
			return err
		}
		var current int
		if err := tx.QueryRowContext(ctx, "SELECT version FROM application_campaigns WHERE id=? AND user_id=? FOR UPDATE", id, user).Scan(&current); err != nil {
			return err
		}
		if version != current {
			return ErrConflict
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM application_campaign_jobs WHERE campaign_id=? AND user_id=?", id, user); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM application_campaigns WHERE id=? AND user_id=?", id, user)
		return err
	})
}
func checkCampaignPlan(ctx context.Context, tx *sql.Tx, user, job string) error {
	rule, err := One[ApplicationCampaign](ctx, tx, "SELECT c.body FROM application_campaigns c JOIN application_campaign_jobs j ON j.campaign_id=c.id AND j.user_id=c.user_id WHERE j.user_id=? AND j.job_id=?", user, job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	planned, submitted, err := campaignUsage(ctx, tx, user, rule.ID)
	if err != nil {
		return err
	}
	if planned+submitted >= rule.Limit {
		return ErrCampaignLimit
	}
	return nil
}
