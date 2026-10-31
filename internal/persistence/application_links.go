package persistence

import (
	"context"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// Read current owned application links in one bounded query, without copying
// mutable relationships into durable event/todo bodies or starting a task.
func (s *Store) applicationLinks(ctx context.Context, user string, jobs []string) (map[string][]d.ApplicationLink, error) {
	out := map[string][]d.ApplicationLink{}
	ids := []string{}
	for _, id := range jobs {
		if len(id) == 32 {
			if _, found := out[id]; !found {
				out[id] = []d.ApplicationLink{}
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 100 {
		return nil, ErrValidation
	}
	args := []any{user}
	for _, id := range ids {
		args = append(args, id)
	}
	// UNIQUE(user_id,job_id) bounds this to one current application per job.
	query := "SELECT job_id,id,JSON_UNQUOTE(JSON_EXTRACT(body,'$.current_state')) FROM applications WHERE user_id=? AND job_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ") LIMIT 100"
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var job string
		var v d.ApplicationLink
		if err := rows.Scan(&job, &v.ID, &v.State); err != nil {
			return nil, err
		}
		out[job] = append(out[job], v)
	}
	return out, rows.Err()
}
