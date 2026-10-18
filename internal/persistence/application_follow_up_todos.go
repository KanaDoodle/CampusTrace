package persistence

import (
	"context"
	"encoding/json"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// Explicit user reminders apply to submitted jobs even when their listing is
// closed or ignored. Only ending the application or completing the reminder
// resolves them. Dates use the same Beijing time as the rest of the workbench.
func applicationFollowUpTodos(ctx context.Context, q Queryer, user string, now time.Time) ([]Todo, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.body,j.body FROM applications a
 LEFT JOIN jobs j ON j.id=a.job_id AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))
 WHERE a.user_id=? AND JSON_UNQUOTE(JSON_EXTRACT(a.body,'$.current_state')) IN ('PLANNED','APPLIED','OA','INTERVIEW','HR')
 AND JSON_EXTRACT(a.body,'$.follow_up_at') IS NOT NULL ORDER BY a.id LIMIT 501`, user, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	beijing := now.In(time.FixedZone("Asia/Shanghai", 8*3600))
	cutoff := time.Date(beijing.Year(), beijing.Month(), beijing.Day()+1, 0, 0, 0, 0, beijing.Location())
	out := []Todo{}
	loaded := 0
	for rows.Next() {
		var body, jobBody []byte
		if err := rows.Scan(&body, &jobBody); err != nil {
			return nil, err
		}
		loaded++
		if loaded > 500 {
			return nil, ErrTodoCapacity
		}
		var app d.Application
		if err := json.Unmarshal(body, &app); err != nil {
			return nil, err
		}
		if app.FollowUpAt == nil || !app.FollowUpAt.Before(cutoff) {
			continue
		}
		item := Todo{ID: "follow-up:" + app.ID, Kind: "APPLICATION_FOLLOW_UP", ApplicationID: app.ID, At: *app.FollowUpAt, NextAction: app.NextAction}
		if len(jobBody) != 0 {
			item.Job = new(ApplicationJob)
			if err := json.Unmarshal(jobBody, item.Job); err != nil {
				return nil, err
			}
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
