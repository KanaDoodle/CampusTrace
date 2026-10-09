package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"sort"
	"time"
)

var ErrRadarCapacity = errors.New("radar capacity: narrow the visible catalog (maximum 500 jobs)")

func (s *Store) SetPreference(ctx context.Context, user, job, value string) error {
	if !d.ValidDisposition(value) {
		return ErrValidation
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if err := CheckJobAccess(ctx, tx, user, job); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO user_job_preferences(user_id,job_id,disposition) VALUES(?,?,?) ON DUPLICATE KEY UPDATE disposition=VALUES(disposition)", user, job, value)
		return err
	})
}
func (s *Store) Preferences(ctx context.Context, user string) ([]d.UserJobPreference, error) {
	if user == "" {
		return nil, ErrNotFound
	}
	return Many[d.UserJobPreference](ctx, s.DB, "SELECT JSON_OBJECT('user_id',user_id,'job_id',job_id,'disposition',disposition) FROM user_job_preferences WHERE user_id=? ORDER BY job_id LIMIT 500", user)
}
func actionable(v d.RadarJob) bool {
	return v.Disposition != "IGNORED" && v.Job.CurrentStatus != "CLOSED" && (v.Application == nil || v.Application.State == "PLANNED")
}
func recommended(v d.RadarJob) bool {
	return actionable(v) && v.Job.CurrentStatus == "OPEN" && (v.Eligibility == "ELIGIBLE" || v.Eligibility == "CONDITIONAL") && v.Ranking.InputIdentity != ""
}

// Deadline identity follows semantic transitions, not each fetch's new evidence IDs.
// Only active processing generations and explicit, confident source deadlines participate.
func radarDeadline(os []d.Observation, es []d.Evidence, now time.Time) (*time.Time, string) {
	current, active := rules.Current(os, es)
	var earliest *time.Time
	version := ""
	for _, o := range current {
		if o.FetchStatus != "SUCCESS" || o.ExtractionStatus != "COMPLETE" || now.Sub(o.ObservedAt) > 7*24*time.Hour {
			continue
		}
		for _, e := range active {
			if e.ObservationID != o.ID || e.Type != "DEADLINE" || e.Confidence < 0.8 {
				continue
			}
			deadline, err := d.DeadlineInstant(e.Value, o.Timezone)
			if err != nil {
				continue
			}
			// Find the start of this uninterrupted deadline value for this posting.
			signature, lastVersion := "", ""
			for _, history := range os {
				if history.PostingID != o.PostingID || history.FetchStatus != "SUCCESS" || history.ExtractionStatus != "COMPLETE" {
					continue
				}
				_, valid := rules.Current([]d.Observation{history}, es)
				values := []string{}
				for _, v := range valid {
					if v.Type == "DEADLINE" && v.Confidence >= 0.8 {
						if at, err := d.DeadlineInstant(v.Value, history.Timezone); err == nil {
							values = append(values, at.UTC().Format(time.RFC3339Nano))
						}
					}
				}
				sort.Strings(values)
				sig := d.JSON(values)
				if sig != signature {
					signature = sig
					lastVersion = history.ID
				}
			}
			if earliest == nil || deadline.Before(*earliest) || (deadline.Equal(*earliest) && lastVersion < version) {
				at := deadline
				earliest = &at
				version = lastVersion
			}
		}
	}
	return earliest, version
}

// One consistent SQL snapshot for status, current evidence, profile, preference and FSM.
func (s *Store) radarJobs(ctx context.Context, q Queryer, user string, now time.Time) ([]d.RadarJob, error) {
	if user == "" {
		return nil, ErrNotFound
	}
	jobs, err := Many[d.Job](ctx, q, "SELECT body FROM jobs WHERE visibility='GLOBAL' OR (visibility='PRIVATE' AND owner_id=?) ORDER BY id LIMIT 501", user)
	if err != nil {
		return nil, err
	}
	if len(jobs) > 500 {
		return nil, ErrRadarCapacity
	}
	return s.radarJobRows(ctx, q, user, now, jobs)
}
func (s *Store) radarJobRows(ctx context.Context, q Queryer, user string, now time.Time, jobs []d.Job) ([]d.RadarJob, error) {
	apps, err := Many[d.Application](ctx, q, "SELECT body FROM applications WHERE user_id=?", user)
	if err != nil {
		return nil, err
	}
	byJob := map[string]*d.Application{}
	for i := range apps {
		byJob[apps[i].JobID] = &apps[i]
	}
	prefs, err := Many[d.UserJobPreference](ctx, q, "SELECT JSON_OBJECT('job_id',job_id,'disposition',disposition) FROM user_job_preferences WHERE user_id=?", user)
	if err != nil {
		return nil, err
	}
	dispositions := map[string]string{}
	for _, v := range prefs {
		dispositions[v.JobID] = v.Disposition
	}
	profile, err := One[d.Profile](ctx, q, "SELECT body FROM profiles WHERE user_id=?", user)
	hasProfile := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out := []d.RadarJob{}
	for _, j := range jobs {
		if dispositions[j.ID] == "IGNORED" {
			continue
		}
		os, err := Many[d.Observation](ctx, q, "SELECT body FROM observations WHERE job_id=? ORDER BY observed_at,id", j.ID)
		if err != nil {
			return nil, err
		}
		es, err := Many[d.Evidence](ctx, q, "SELECT body FROM evidence WHERE job_id=? ORDER BY id", j.ID)
		if err != nil {
			return nil, err
		}
		originalJob := j
		current, ev := rules.Current(os, es)
		status := rules.Status(j.ID, current, ev, now)
		j.CurrentStatus = status.Status
		v := d.RadarJob{Job: j, Eligibility: "UNKNOWN", Disposition: "NONE", Application: byJob[j.ID]}
		if value := dispositions[j.ID]; value != "" {
			v.Disposition = value
		}
		if hasProfile {
			evaluation := s.evaluateInputs(originalJob, profile, os, es, now)
			v.Eligibility = evaluation.Eligibility.Status
			v.Ranking = evaluation.Ranking
		}
		v.Deadline, v.DeadlineVersion = radarDeadline(os, es, now)
		out = append(out, v)
	}
	return out, nil
}
func recentChanges(ctx context.Context, q Queryer, user string, days int, now time.Time, limits ...int) ([]d.ChangeItem, error) {
	if user == "" {
		return nil, ErrNotFound
	}
	if days != 1 && days != 7 {
		return nil, ErrValidation
	}
	limit := 100
	if len(limits) > 0 {
		limit = limits[0]
	}
	since := now.Add(-time.Duration(days) * 24 * time.Hour)
	// The SQL read model derives status transitions from the existing assessment history.
	return Many[d.ChangeItem](ctx, q, `WITH visible AS (
 SELECT j.* FROM jobs j LEFT JOIN user_job_preferences p ON p.job_id=j.id AND p.user_id=?
 WHERE (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?)) AND COALESCE(p.disposition,'NONE')<>'IGNORED'
 ), status_history AS (
 SELECT a.id,a.job_id,a.assessed_at,a.body,LAG(JSON_UNQUOTE(JSON_EXTRACT(a.body,'$.status'))) OVER(PARTITION BY a.job_id ORDER BY a.assessed_at,a.id) previous
 FROM assessments a JOIN visible j ON j.id=a.job_id
 ), events AS (
 SELECT JSON_OBJECT('id',j.id,'job_id',j.id,'title',JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.title')),'type','NEW_JOB','created_at',JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.created_at'))) body FROM visible j
 UNION ALL
 SELECT JSON_OBJECT('id',c.id,'job_id',c.job_id,'title',JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.title')),'type',IF(JSON_UNQUOTE(JSON_EXTRACT(c.body,'$.type'))='JD_CONTENT_CHANGED','JOB_CONTENT_CHANGED','DEADLINE_CHANGED'),'from',JSON_UNQUOTE(JSON_EXTRACT(c.body,'$.from_observation')),'to',JSON_UNQUOTE(JSON_EXTRACT(c.body,'$.to_observation')),'created_at',JSON_UNQUOTE(JSON_EXTRACT(c.body,'$.created_at')))
 FROM changes c JOIN visible j ON j.id=c.job_id JOIN observations o ON o.id=JSON_UNQUOTE(JSON_EXTRACT(c.body,'$.to_observation'))
 WHERE JSON_UNQUOTE(JSON_EXTRACT(c.body,'$.type')) IN ('JD_CONTENT_CHANGED','DEADLINE_CHANGED')
 AND JSON_UNQUOTE(JSON_EXTRACT(o.body,'$.extraction_status'))='COMPLETE'
 AND COALESCE(JSON_EXTRACT(o.body,'$.active_analysis_generation'),0)=COALESCE(JSON_EXTRACT(o.body,'$.current_analysis_generation'),0)
 UNION ALL
 SELECT JSON_OBJECT('id',a.id,'job_id',a.job_id,'title',JSON_UNQUOTE(JSON_EXTRACT(j.body,'$.title')),'type','JOB_STATUS_CHANGED','from',a.previous,'to',JSON_UNQUOTE(JSON_EXTRACT(a.body,'$.status')),'created_at',JSON_UNQUOTE(JSON_EXTRACT(a.body,'$.assessed_at')))
 FROM status_history a JOIN visible j ON j.id=a.job_id WHERE a.previous IS NOT NULL AND a.previous<>JSON_UNQUOTE(JSON_EXTRACT(a.body,'$.status'))
 ), timed AS (
 SELECT body, CONVERT_TZ(
 CAST(REPLACE(REGEXP_REPLACE(JSON_UNQUOTE(JSON_EXTRACT(body,'$.created_at')),'(Z|[+-][0-9]{2}:[0-9]{2})$',''),'T',' ') AS DATETIME(6)),
 IF(RIGHT(JSON_UNQUOTE(JSON_EXTRACT(body,'$.created_at')),1)='Z','+00:00',RIGHT(JSON_UNQUOTE(JSON_EXTRACT(body,'$.created_at')),6)), '+00:00') event_at FROM events
 ) SELECT body FROM timed WHERE event_at>=? AND event_at<=? ORDER BY event_at DESC,JSON_UNQUOTE(JSON_EXTRACT(body,'$.id')) LIMIT ?`, user, user, since.UTC(), now.UTC(), limit)
}
func (s *Store) RecentChanges(ctx context.Context, user string, days int) ([]d.ChangeItem, error) {
	return recentChanges(ctx, s.DB, user, days, time.Now().UTC())
}
func (s *Store) ClosingJobs(ctx context.Context, user string, days int) ([]d.RadarJob, error) {
	if days != 3 && days != 7 && days != 14 {
		return nil, ErrValidation
	}
	var out []d.RadarJob
	err := s.radarSnapshot(ctx, func(q Queryer, now time.Time) error {
		// Narrow by any plausible deadline first, then apply the identical current
		// evidence/status checks. Unrelated catalog size cannot block this query.
		candidates, err := Many[d.Job](ctx, q, `SELECT j.body FROM jobs j WHERE (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?)) AND EXISTS (SELECT 1 FROM evidence e WHERE e.job_id=j.id AND JSON_UNQUOTE(JSON_EXTRACT(e.body,'$.type'))='DEADLINE' AND JSON_UNQUOTE(JSON_EXTRACT(e.body,'$.value'))>=? AND JSON_UNQUOTE(JSON_EXTRACT(e.body,'$.value'))<=?) ORDER BY j.id LIMIT 501`, user, now.Add(-24*time.Hour).Format("2006-01-02"), now.Add(time.Duration(days+1)*24*time.Hour).Format("2006-01-02"))
		if err != nil {
			return err
		}
		if len(candidates) > 500 {
			return ErrRadarCapacity
		}
		jobs, err := s.radarJobRows(ctx, q, user, now, candidates)
		if err != nil {
			return err
		}
		out = closing(jobs, now, days)
		return nil
	})
	return out, err
}
func closing(jobs []d.RadarJob, now time.Time, days int) []d.RadarJob {
	out := []d.RadarJob{}
	for _, v := range jobs {
		if actionable(v) && v.Deadline != nil && v.Deadline.After(now) && !v.Deadline.After(now.Add(time.Duration(days)*24*time.Hour)) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Deadline.Equal(*out[j].Deadline) {
			return out[i].Job.ID < out[j].Job.ID
		}
		return out[i].Deadline.Before(*out[j].Deadline)
	})
	return out
}
func (s *Store) radarSnapshot(ctx context.Context, fn func(Queryer, time.Time) error) error {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}
func upcoming(ctx context.Context, q Queryer, user string, now time.Time) ([]d.Interview, error) {
	rows, err := Many[d.Interview](ctx, q, `SELECT i.body FROM interviews i JOIN applications a ON a.id=i.application_id WHERE i.user_id=? AND JSON_EXTRACT(i.body,'$.finished_at') IS NULL AND JSON_UNQUOTE(JSON_EXTRACT(a.body,'$.current_state')) NOT IN ('OFFER','REJECTED','WITHDRAWN') ORDER BY i.id LIMIT 501`, user)
	if err != nil {
		return nil, err
	}
	if len(rows) > 500 {
		return nil, ErrRadarCapacity
	}
	out := []d.Interview{}
	for _, i := range rows {
		if !i.ScheduledAt.Before(now) && !i.ScheduledAt.After(now.Add(7*24*time.Hour)) {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ScheduledAt.Equal(out[j].ScheduledAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].ScheduledAt.Before(out[j].ScheduledAt)
	})
	return out, nil
}

func (s *Store) DailyDigest(ctx context.Context, user string) (d.DailyDigest, error) {
	v := d.DailyDigest{NewJobs: []d.RadarJob{}, RecommendedJobs: []d.RadarJob{}, StatusChanges: []d.ChangeItem{}}
	err := s.radarSnapshot(ctx, func(q Queryer, now time.Time) error {
		v.AsOf = now
		jobs, err := s.radarJobs(ctx, q, user, now)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Job.CreatedAt.After(now.Add(-24 * time.Hour)) {
				v.NewJobs = append(v.NewJobs, j)
			}
			if recommended(j) {
				v.RecommendedJobs = append(v.RecommendedJobs, j)
			}
		}
		sort.Slice(v.NewJobs, func(i, j int) bool {
			if v.NewJobs[i].Job.CreatedAt.Equal(v.NewJobs[j].Job.CreatedAt) {
				return v.NewJobs[i].Job.ID < v.NewJobs[j].Job.ID
			}
			return v.NewJobs[i].Job.CreatedAt.After(v.NewJobs[j].Job.CreatedAt)
		})
		sort.Slice(v.RecommendedJobs, func(i, j int) bool {
			if v.RecommendedJobs[i].Ranking.Score == v.RecommendedJobs[j].Ranking.Score {
				return v.RecommendedJobs[i].Job.ID < v.RecommendedJobs[j].Job.ID
			}
			return v.RecommendedJobs[i].Ranking.Score > v.RecommendedJobs[j].Ranking.Score
		})
		v.ClosingSoon = closing(jobs, now, 7)
		v.RecentChanges, err = recentChanges(ctx, q, user, 1, now, 10001)
		if err != nil {
			return err
		}
		for _, c := range v.RecentChanges {
			if c.Type == "JOB_STATUS_CHANGED" {
				v.StatusChanges = append(v.StatusChanges, c)
			}
		}
		v.UpcomingInterviews, err = upcoming(ctx, q, user, now)
		if err != nil {
			return err
		}
		if len(v.RecentChanges) > 10000 {
			return ErrRadarCapacity
		}
		v.Counts = map[string]int{"new_jobs": len(v.NewJobs), "recommended_jobs": len(v.RecommendedJobs), "closing_soon": len(v.ClosingSoon), "status_changes": len(v.StatusChanges), "upcoming_interviews": len(v.UpcomingInterviews)}
		v.Truncated = len(v.NewJobs) > 5 || len(v.RecommendedJobs) > 5 || len(v.ClosingSoon) > 5 || len(v.StatusChanges) > 10 || len(v.RecentChanges) > 10 || len(v.UpcomingInterviews) > 10
		v.NewJobs = v.NewJobs[:min(5, len(v.NewJobs))]
		v.RecommendedJobs = v.RecommendedJobs[:min(5, len(v.RecommendedJobs))]
		v.ClosingSoon = v.ClosingSoon[:min(5, len(v.ClosingSoon))]
		v.StatusChanges = v.StatusChanges[:min(10, len(v.StatusChanges))]
		v.RecentChanges = v.RecentChanges[:min(10, len(v.RecentChanges))]
		v.UpcomingInterviews = v.UpcomingInterviews[:min(10, len(v.UpcomingInterviews))]
		return nil
	})
	return v, err
}
func notification(ctx context.Context, tx *sql.Tx, n d.Notification) (bool, error) {
	n.ID = d.ID()
	n.CreatedAt = time.Now().UTC()
	key := d.Hash(d.JSON([]string{n.Type, n.EntityType, n.EntityID, n.EventVersion}))
	_, err := tx.ExecContext(ctx, "INSERT INTO notifications(id,user_id,dedup_key,created_at,body) VALUES(?,?,?,?,?)", n.ID, n.UserID, key, n.CreatedAt, d.JSON(n))
	if duplicate(err) {
		return false, nil
	}
	return err == nil, err
}

// Inbox materialization uses a fresh repeatable snapshot and SQL unique keys. It
// is an explicit write path, never invoked by Agent read tools or digest GETs.
func (s *Store) RefreshNotifications(ctx context.Context, user string) (created, deduplicated int, err error) {
	tx, e := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return 0, 0, e
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	jobs, e := s.radarJobs(ctx, tx, user, now)
	if e != nil {
		return 0, 0, e
	}
	add := func(typ, entity, id, version, title, body string) error {
		ok, e := notification(ctx, tx, d.Notification{UserID: user, Type: typ, EntityType: entity, EntityID: id, EventVersion: version, Title: title, Body: body})
		if e == nil {
			if ok {
				created++
			} else {
				deduplicated++
			}
		}
		return e
	}
	for _, j := range jobs {
		if recommended(j) && j.Ranking.Score >= 70 && j.Job.CreatedAt.After(now.Add(-24*time.Hour)) {
			if e = add("NEW_HIGH_PRIORITY_JOB", "JOB", j.Job.ID, "first-priority", "新增优先投递岗位", j.Job.Company+" · "+j.Job.Title); e != nil {
				return 0, 0, e
			}
		}
		if actionable(j) && j.Deadline != nil && j.Deadline.After(now) {
			hours := j.Deadline.Sub(now).Hours()
			window := ""
			switch {
			case hours <= 24:
				window = "D1"
			case hours <= 72:
				window = "D3"
			case hours <= 168:
				window = "D7"
			}
			if window != "" {
				if e = add("JOB_CLOSING_SOON", "JOB", j.Job.ID, j.DeadlineVersion+":"+window, "岗位即将截止", fmt.Sprintf("%s · %s · %s", j.Job.Title, map[string]string{"D1": "1 天内截止", "D3": "3 天内截止", "D7": "7 天内截止"}[window], radarDate(*j.Deadline))); e != nil {
					return 0, 0, e
				}
			}
		}
	}
	changes, e := recentChanges(ctx, tx, user, 7, now, 10001)
	if e != nil {
		return 0, 0, e
	}
	if len(changes) > 10000 {
		return 0, 0, ErrRadarCapacity
	}
	for _, c := range changes {
		if c.Type == "JOB_STATUS_CHANGED" {
			if e = add(c.Type, "JOB", c.JobID, c.ID, "岗位状态变化", c.Title+" · "+radarStatus(c.From)+" → "+radarStatus(c.To)); e != nil {
				return 0, 0, e
			}
		}
	}
	interviews, e := upcoming(ctx, tx, user, now)
	if e != nil {
		return 0, 0, e
	}
	for _, v := range interviews {
		if e = add("INTERVIEW_UPCOMING", "INTERVIEW", v.ID, v.ScheduledAt.Format(time.RFC3339Nano), "近期面试安排", fmt.Sprintf("第 %d 轮 · %s", v.Round, radarDate(v.ScheduledAt))); e != nil {
			return 0, 0, e
		}
	}
	err = tx.Commit()
	return
}
func (s *Store) Notifications(ctx context.Context, user string) ([]d.Notification, error) {
	if user == "" {
		return nil, ErrNotFound
	}
	return Many[d.Notification](ctx, s.DB, "SELECT body FROM notifications WHERE user_id=? ORDER BY created_at DESC,id LIMIT 100", user)
}
func (s *Store) ReadNotification(ctx context.Context, user, id string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		v, err := One[d.Notification](ctx, tx, "SELECT body FROM notifications WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if err != nil {
			return err
		}
		if v.ReadAt != nil {
			return nil
		}
		now := time.Now().UTC()
		v.ReadAt = &now
		_, err = tx.ExecContext(ctx, "UPDATE notifications SET read_at=?,body=? WHERE id=? AND user_id=?", now, d.JSON(v), id, user)
		return err
	})
}

func radarStatus(v string) string {
	switch v {
	case "OPEN":
		return "可投递"
	case "CLOSED":
		return "已关闭"
	case "UNKNOWN":
		return "暂无法确认"
	case "NEEDS_VERIFICATION":
		return "待核验"
	}
	return "状态待确认"
}
func radarDate(v time.Time) string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return v.UTC().Format(time.RFC3339)
	}
	return v.In(loc).Format("2006-01-02 15:04（北京时间）")
}
