package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
)

var ErrTodoCapacity = errors.New("todo capacity exceeded")

type Todo struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Job           *ApplicationJob `json:"job,omitempty"`
	ApplicationID string          `json:"application_id,omitempty"`
	InterviewID   string          `json:"interview_id,omitempty"`
	TaskID        string          `json:"task_id,omitempty"`
	Round         int             `json:"round,omitempty"`
	At            time.Time       `json:"at"`
}
type TodoSnapshot struct {
	AsOf      time.Time       `json:"as_of"`
	Counts    map[string]int  `json:"counts"`
	Items     []Todo          `json:"items"`
	Truncated map[string]bool `json:"truncated"`
}

// This projection is read-only and independent of Radar's global catalog cap.
// No candidate content, credentials, retry, notification write or model call.
func (s *Store) Todos(ctx context.Context, user string) (TodoSnapshot, error) {
	v := TodoSnapshot{Counts: map[string]int{"PLANNED_CLOSING": 0, "INTERVIEW_UPCOMING": 0, "REVIEW_PENDING": 0, "ANALYSIS_FAILED": 0}, Items: []Todo{}, Truncated: map[string]bool{}}
	if user == "" {
		return v, ErrNotFound
	}
	err := s.radarSnapshot(ctx, func(q Queryer, now time.Time) error {
		v.AsOf = now
		plans, err := plannedTodos(ctx, q, user, now)
		if err != nil {
			return err
		}
		all := plans
		interviews, err := interviewRecords(ctx, q, user, 1001)
		if err != nil {
			return err
		}
		if len(interviews) > 1000 {
			return ErrTodoCapacity
		}
		applications, err := Many[MatchApplication](ctx, q, `SELECT JSON_OBJECT('id',id,'current_state',JSON_EXTRACT(body,'$.current_state')) FROM applications WHERE user_id=?`, user)
		if err != nil {
			return err
		}
		states := map[string]string{}
		for _, a := range applications {
			states[a.ID] = a.State
		}
		for _, i := range interviews {
			item := Todo{ID: i.ID, Job: i.Job, InterviewID: i.ID, ApplicationID: i.ApplicationID, Round: i.Round}
			if i.FinishedAt != nil && !i.FinishedAt.IsZero() {
				if i.Review == nil {
					item.Kind, item.At = "REVIEW_PENDING", *i.FinishedAt
					all = append(all, item)
				}
			} else if state := states[i.ApplicationID]; state != "OFFER" && state != "REJECTED" && state != "WITHDRAWN" && !i.ScheduledAt.Before(now) && !i.ScheduledAt.After(now.Add(7*24*time.Hour)) {
				item.Kind, item.At = "INTERVIEW_UPCOMING", i.ScheduledAt
				all = append(all, item)
			}
		}
		failures, err := failedTodos(ctx, q, user)
		if err != nil {
			return err
		}
		all = append(all, failures...)
		sort.Slice(all, func(i, j int) bool {
			priority := map[string]int{"PLANNED_CLOSING": 0, "INTERVIEW_UPCOMING": 1, "REVIEW_PENDING": 2, "ANALYSIS_FAILED": 3}
			a, b := all[i], all[j]
			if a.Kind != b.Kind {
				return priority[a.Kind] < priority[b.Kind]
			}
			if !a.At.Equal(b.At) {
				if a.Kind == "PLANNED_CLOSING" || a.Kind == "INTERVIEW_UPCOMING" {
					return a.At.Before(b.At)
				}
				return a.At.After(b.At)
			}
			return a.ID < b.ID
		})
		for _, item := range all {
			v.Counts[item.Kind]++
			if v.Counts[item.Kind] <= 5 {
				v.Items = append(v.Items, item)
			} else {
				v.Truncated[item.Kind] = true
			}
		}
		return nil
	})
	return v, err
}

func plannedTodos(ctx context.Context, q Queryer, user string, now time.Time) ([]Todo, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.id,j.body FROM applications a JOIN jobs j ON j.id=a.job_id
 LEFT JOIN user_job_preferences p ON p.user_id=a.user_id AND p.job_id=a.job_id
 WHERE a.user_id=? AND JSON_UNQUOTE(JSON_EXTRACT(a.body,'$.current_state'))='PLANNED'
 AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?)) AND COALESCE(p.disposition,'NONE')<>'IGNORED'
 ORDER BY a.id LIMIT 501`, user, user)
	if err != nil {
		return nil, err
	}
	type plan struct {
		id  string
		job d.Job
	}
	plans := []plan{}
	for rows.Next() {
		var p plan
		var body []byte
		if err = rows.Scan(&p.id, &body); err == nil {
			err = json.Unmarshal(body, &p.job)
		}
		if err != nil {
			rows.Close()
			return nil, err
		}
		plans = append(plans, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(plans) > 500 {
		return nil, ErrTodoCapacity
	}
	osByJob, esByJob := map[string][]d.Observation{}, map[string][]d.Evidence{}
	for start := 0; start < len(plans); start += 250 {
		end := min(start+250, len(plans))
		slots, args := []string{}, []any{}
		for _, p := range plans[start:end] {
			slots, args = append(slots, "?"), append(args, p.job.ID)
		}
		where := " WHERE job_id IN (" + strings.Join(slots, ",") + ")"
		os, err := Many[d.Observation](ctx, q, "SELECT body FROM observations"+where+" ORDER BY observed_at,id", args...)
		if err != nil {
			return nil, err
		}
		es, err := Many[d.Evidence](ctx, q, "SELECT body FROM evidence"+where+" ORDER BY id", args...)
		if err != nil {
			return nil, err
		}
		for _, o := range os {
			osByJob[o.JobID] = append(osByJob[o.JobID], o)
		}
		for _, e := range es {
			esByJob[e.JobID] = append(esByJob[e.JobID], e)
		}
	}
	out := []Todo{}
	for _, p := range plans {
		os, es := osByJob[p.job.ID], esByJob[p.job.ID]
		at, _ := radarDeadline(os, es, now)
		if at == nil || !at.After(now) || at.After(now.Add(7*24*time.Hour)) {
			continue
		}
		current, evidence := rules.Current(os, es)
		if rules.Status(p.job.ID, current, evidence, now).Status == "CLOSED" {
			continue
		}
		j := p.job
		job := &ApplicationJob{CompanyID: j.CompanyID, ID: j.ID, Company: j.Company, Title: j.Title, Locations: j.Locations, JobType: j.JobType, CurrentStatus: j.CurrentStatus}
		out = append(out, Todo{ID: p.id, Kind: "PLANNED_CLOSING", Job: job, ApplicationID: p.id, At: *at})
	}
	return out, nil
}

func failedTodos(ctx context.Context, q Queryer, user string) ([]Todo, error) {
	// A newer attempt (including success or cancellation) supersedes the old
	// failed item. Synchronous successful analysis also resolves old failures.
	rows, err := q.QueryContext(ctx, `WITH latest AS (
 SELECT r.id,r.state,r.updated_at,items.job_id,items.item_state,
 ROW_NUMBER() OVER (PARTITION BY items.job_id ORDER BY JSON_UNQUOTE(JSON_EXTRACT(r.body,'$.created_at')) DESC,r.id DESC) AS position
 FROM match_runs r JOIN JSON_TABLE(r.body,'$.items[*]' COLUMNS(job_id VARCHAR(32) PATH '$.job_id',item_state VARCHAR(24) PATH '$.state')) items
 WHERE r.user_id=?
 ) SELECT l.id,l.job_id,l.updated_at,j.body,JSON_UNQUOTE(JSON_EXTRACT(m.body,'$.analyzed_at')) FROM latest l JOIN jobs j ON j.id=CAST(l.job_id AS BINARY)
 LEFT JOIN user_job_preferences p ON p.user_id=? AND p.job_id=j.id
 LEFT JOIN job_match_results m ON m.user_id=? AND m.job_id=j.id
 WHERE l.position=1 AND l.item_state='FAILED' AND l.state NOT IN ('RUNNING','PAUSING','CANCELLED')
 AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))
 AND COALESCE(p.disposition,'NONE')<>'IGNORED' AND JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.current_status'))<>'CLOSED'

 ORDER BY l.updated_at DESC,l.job_id LIMIT 501`, user, user, user, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Todo{}
	loaded := 0
	for rows.Next() {
		item := Todo{Kind: "ANALYSIS_FAILED", Job: new(ApplicationJob)}
		var jobID string
		var analyzed sql.NullString
		var body []byte
		if err := rows.Scan(&item.TaskID, &jobID, &item.At, &body, &analyzed); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, item.Job); err != nil {
			return nil, err
		}
		loaded++
		if analyzed.Valid {
			if at, err := time.Parse(time.RFC3339Nano, analyzed.String); err == nil && !at.Before(item.At) {
				continue
			}
		}
		item.ID = item.TaskID + ":" + jobID
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if loaded > 500 {
		return nil, ErrTodoCapacity
	}
	return out, nil
}
