package persistence

import (
	"context"
	"database/sql"
	"strings"
	"unicode/utf8"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type PreferenceJob struct {
	Job         d.Job          `json:"job"`
	Disposition string         `json:"disposition"`
	Application *d.Application `json:"application,omitempty"`
}

type PreferenceJobsPage struct {
	Jobs     []PreferenceJob `json:"jobs"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// Browse saved/ignored metadata without a profile, assessments, evidence or model calls.
// Count and rows share one visibility-checked snapshot; the legacy 500-record
// preference endpoint is not used to approximate the size of this collection.
func (s *Store) PreferenceJobs(ctx context.Context, user, disposition, query, order string, page int) (PreferenceJobsPage, error) {
	out := PreferenceJobsPage{Jobs: []PreferenceJob{}, Page: page, PageSize: 50}
	if user == "" {
		return out, ErrNotFound
	}
	query = strings.TrimSpace(query)
	orders := map[string]string{
		"updated": "JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.updated_at')) DESC,j.id",
		"company": "JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.company')),JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.title')),j.id",
		"title":   "JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.title')),j.id",
	}
	ordering, ok := orders[order]
	if !ok || page < 1 || page > 20000 || utf8.RuneCountInString(query) > 200 || disposition != "SAVED" && disposition != "IGNORED" {
		return out, ErrValidation
	}
	where := " FROM user_job_preferences p JOIN jobs j ON j.id=p.job_id WHERE p.user_id=? AND p.disposition=? AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))"
	args := []any{user, disposition, user}
	escape := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	for _, word := range strings.Fields(strings.ToLower(query)) {
		where += " AND LOWER(CONCAT_WS(' ',JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.company')),JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.title')),JSON_EXTRACT(j.body,'$.locations'))) LIKE ? ESCAPE '!'"
		args = append(args, "%"+escape.Replace(word)+"%")
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*)"+where, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	last := max(1, (out.Total+out.PageSize-1)/out.PageSize)
	if last > 20000 {
		return out, ErrRadarCapacity
	}
	out.Page = min(page, last)
	// Applications are loaded only for the returned page and remain owner-scoped.
	rows, err := Many[d.Job](ctx, tx, "SELECT j.body"+where+" ORDER BY "+ordering+" LIMIT 50 OFFSET ?", append(args, (out.Page-1)*out.PageSize)...)
	if err != nil {
		return out, err
	}
	if len(rows) > 0 {
		slots, appArgs := []string{}, []any{user}
		for _, job := range rows {
			slots, appArgs = append(slots, "?"), append(appArgs, job.ID)
		}
		apps, err := Many[d.Application](ctx, tx, "SELECT body FROM applications WHERE user_id=? AND job_id IN ("+strings.Join(slots, ",")+")", appArgs...)
		if err != nil {
			return out, err
		}
		byJob := map[string]*d.Application{}
		for i := range apps {
			byJob[apps[i].JobID] = &apps[i]
		}
		for _, job := range rows {
			out.Jobs = append(out.Jobs, PreferenceJob{Job: job, Disposition: disposition, Application: byJob[job.ID]})
		}
	}
	return out, tx.Commit()
}
