package persistence

import (
	"context"
	"encoding/json"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type InterviewRecord struct {
	d.Interview
	Job    *ApplicationJob `json:"job,omitempty"`
	Review *d.Review       `json:"review,omitempty"`
}

// Owning an interview does not grant access to a job that is now private to
// someone else. Reviews are joined under the same owner as the interview.
func (s *Store) InterviewRecords(ctx context.Context, user string) ([]InterviewRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT i.body,j.body,r.body
 FROM interviews i JOIN applications a ON a.id=i.application_id AND a.user_id=i.user_id
 LEFT JOIN jobs j ON j.id=a.job_id AND (j.visibility='GLOBAL' OR (j.visibility='PRIVATE' AND j.owner_id=?))
 LEFT JOIN reviews r ON r.interview_id=i.id AND r.user_id=i.user_id
 WHERE i.user_id=? ORDER BY JSON_UNQUOTE(JSON_EXTRACT(i.body,'$.scheduled_at')) DESC,i.id LIMIT 500`, user, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InterviewRecord{}
	for rows.Next() {
		var body, job, review []byte
		if err := rows.Scan(&body, &job, &review); err != nil {
			return nil, err
		}
		var v InterviewRecord
		if err := json.Unmarshal(body, &v.Interview); err != nil {
			return nil, err
		}
		if len(job) > 0 {
			v.Job = new(ApplicationJob)
			if err := json.Unmarshal(job, v.Job); err != nil {
				return nil, err
			}
		}
		if len(review) > 0 {
			v.Review = new(d.Review)
			if err := json.Unmarshal(review, v.Review); err != nil {
				return nil, err
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
