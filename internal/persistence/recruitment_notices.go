package persistence

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	mail "github.com/KanaDoodle/CampusTrace/internal/recruitmentmail"
)

type NoticeCandidate struct {
	Application ApplicationRecord `json:"application"`
	Reasons     []string          `json:"reasons"`
	Strength    int               `json:"strength"`
}
type RecruitmentNotice struct {
	ID               string              `json:"id"`
	Version          int                 `json:"version"`
	State            string              `json:"state"`
	ContentHash      string              `json:"content_hash"`
	CreatedAt        time.Time           `json:"created_at"`
	Parsed           mail.Parsed         `json:"parsed"`
	ApplicationID    string              `json:"application_id,omitempty"`
	Confirmed        *NoticeConfirmation `json:"confirmed,omitempty"`
	ConfirmationHash string              `json:"confirmation_hash,omitempty"`
	InterviewID      string              `json:"interview_id,omitempty"`
}
type NoticeView struct {
	Notice     RecruitmentNotice `json:"notice"`
	Candidates []NoticeCandidate `json:"candidates"`
	Ambiguous  bool              `json:"ambiguous"`
}

func noticeCandidates(p mail.Parsed, rows []ApplicationRecord) []NoticeCandidate {
	out := []NoticeCandidate{}
	text := strings.ToLower(p.Subject + "\n" + p.Text)
	for _, a := range rows {
		v := NoticeCandidate{Application: a, Reasons: []string{}}
		if p.Reference != "" && a.ExternalReference != "" && strings.EqualFold(p.Reference, a.ExternalReference) {
			v.Strength += 100
			v.Reasons = append(v.Reasons, "申请编号完全一致")
		}
		if a.Job != nil {
			if a.Job.Company != "" && strings.Contains(text, strings.ToLower(a.Job.Company)) {
				v.Strength += 20
				v.Reasons = append(v.Reasons, "正文包含公司名称")
			}
			if a.Job.Title != "" && strings.Contains(text, strings.ToLower(a.Job.Title)) {
				v.Strength += 40
				v.Reasons = append(v.Reasons, "正文包含岗位名称")
			}
		}
		if v.Strength > 0 {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Strength != out[j].Strength {
			return out[i].Strength > out[j].Strength
		}
		return out[i].Application.ID < out[j].Application.ID
	})
	return out
}
func (s *Store) ImportRecruitmentNotice(ctx context.Context, user, content, format string) (NoticeView, error) {
	p, e := mail.Parse(content, format)
	if e != nil {
		return NoticeView{}, ErrValidation
	}
	hash := d.Hash(p.Subject + "\n" + p.From + "\n" + p.Text)
	dedup := hash
	if p.MessageID != "" {
		dedup = d.Hash("message-id:" + p.MessageID)
	}
	v := RecruitmentNotice{ID: d.ID(), Version: 1, State: "PENDING", ContentHash: hash, CreatedAt: time.Now().UTC(), Parsed: p}
	e = s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		old, e := One[RecruitmentNotice](ctx, tx, "SELECT body FROM recruitment_notices WHERE user_id=? AND dedup_key=? FOR UPDATE", user, dedup)
		if e == nil {
			if old.ContentHash != hash {
				return ErrConflict
			}
			v = old
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		var count int
		if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM recruitment_notices WHERE user_id=?", user).Scan(&count); e != nil {
			return e
		}
		if count >= 2000 {
			return ErrValidation
		}
		_, e = tx.ExecContext(ctx, "INSERT INTO recruitment_notices(id,user_id,dedup_key,state,version,created_at,body) VALUES(?,?,?,?,?,?,?)", v.ID, user, dedup, v.State, v.Version, v.CreatedAt, d.JSON(v))
		return e
	})
	if e != nil {
		return NoticeView{}, e
	}
	return s.RecruitmentNotice(ctx, user, v.ID)
}
func (s *Store) RecruitmentNotices(ctx context.Context, user string) ([]RecruitmentNotice, error) {
	return Many[RecruitmentNotice](ctx, s.DB, "SELECT body FROM recruitment_notices WHERE user_id=? ORDER BY created_at DESC,id DESC LIMIT 100", user)
}
func (s *Store) RecruitmentNotice(ctx context.Context, user, id string) (NoticeView, error) {
	v := NoticeView{Candidates: []NoticeCandidate{}}
	if len(id) != 32 {
		return v, ErrValidation
	}
	var e error
	v.Notice, e = One[RecruitmentNotice](ctx, s.DB, "SELECT body FROM recruitment_notices WHERE user_id=? AND id=?", user, id)
	if e != nil {
		return v, e
	}
	rows, e := s.ApplicationRecords(ctx, user)
	if e != nil {
		return v, e
	}
	v.Candidates = noticeCandidates(v.Notice.Parsed, rows)
	v.Ambiguous = len(v.Candidates) != 1
	// A strong reference can narrow suggestions, but never performs a write.
	return v, nil
}

type NoticeConfirmation struct {
	Version             int        `json:"version"`
	ApplicationID       string     `json:"application_id"`
	ApplicationVersion  int        `json:"application_version"`
	Kind                string     `json:"kind"`
	ScheduledAt         *time.Time `json:"scheduled_at,omitempty"`
	Round               int        `json:"round"`
	InterviewID         string     `json:"interview_id,omitempty"`
	PreviousScheduledAt *time.Time `json:"previous_scheduled_at,omitempty"`
	Reference           string     `json:"reference"`
	Note                string     `json:"note"`
}

func validateNoticeConfirmation(in NoticeConfirmation) error {
	if in.Version < 1 || len(in.ApplicationID) != 32 || in.ApplicationVersion < 1 || len(in.Reference) > 80 || len(in.Note) > 2000 {
		return ErrValidation
	}
	if strings.ContainsAny(in.Reference, "\r\n\x00") {
		return ErrValidation
	}
	switch in.Kind {
	case "UNKNOWN", "OA", "INTERVIEW", "RESCHEDULE", "OFFER", "REJECTED":
	default:
		return ErrValidation
	}
	if in.ScheduledAt != nil && (in.ScheduledAt.Year() < 2000 || in.ScheduledAt.Year() > 2100) {
		return ErrValidation
	}
	if in.Kind == "INTERVIEW" || in.Kind == "RESCHEDULE" {
		if in.ScheduledAt == nil || in.Round < 1 || in.Round > 20 {
			return ErrValidation
		}
	}
	if in.Kind == "RESCHEDULE" && (len(in.InterviewID) != 32 || in.PreviousScheduledAt == nil) {
		return ErrValidation
	}
	if in.Kind != "RESCHEDULE" && (in.InterviewID != "" || in.PreviousScheduledAt != nil) {
		return ErrValidation
	}
	return nil
}
func (s *Store) ConfirmRecruitmentNotice(ctx context.Context, user, id string, in NoticeConfirmation) (RecruitmentNotice, error) {
	out := RecruitmentNotice{}
	in.Reference = strings.TrimSpace(in.Reference)
	in.Note = strings.TrimSpace(in.Note)
	if len(id) != 32 || validateNoticeConfirmation(in) != nil {
		return out, ErrValidation
	}
	hash := d.Hash(d.JSON(in))
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if e := lockRunUser(ctx, tx, user); e != nil {
			return e
		}
		v, e := One[RecruitmentNotice](ctx, tx, "SELECT body FROM recruitment_notices WHERE id=? AND user_id=? FOR UPDATE", id, user)
		if e != nil {
			return e
		}
		if v.State == "CONFIRMED" {
			if v.ConfirmationHash != hash {
				return ErrConflict
			}
			out = v
			return nil
		}
		if v.Version != in.Version || v.State != "PENDING" {
			return ErrConflict
		}
		a, e := One[d.Application](ctx, tx, "SELECT body FROM applications WHERE id=? AND user_id=? FOR UPDATE", in.ApplicationID, user)
		if e != nil {
			return e
		}
		if a.Version != in.ApplicationVersion {
			return ErrConflict
		}
		if e = CheckJobAccess(ctx, tx, user, a.JobID); e != nil {
			return e
		}
		if in.Reference != "" && a.ExternalReference != "" && !strings.EqualFold(a.ExternalReference, in.Reference) {
			return ErrConflict
		}
		old := a.State
		target := in.Kind
		if target == "RESCHEDULE" {
			target = "INTERVIEW"
		}
		// Allow notices for later rounds without regressing or skipping state transitions.
		if target != "UNKNOWN" && target != a.State {
			if d.Transition(a.State, target) == nil {
				a.State = target
			} else if target == "INTERVIEW" && (a.State == "HR" || a.State == "OFFER") { /* keep later stage */
			} else {
				return ErrValidation
			}
		}
		if (in.Kind == "OA" || in.Kind == "INTERVIEW" || in.Kind == "RESCHEDULE") && a.AppliedAt == nil {
			return ErrValidation
		}
		now := time.Now().UTC()
		if in.Reference != "" {
			a.ExternalReference = in.Reference
		}
		if in.Kind == "INTERVIEW" || in.Kind == "RESCHEDULE" {
			interview := d.Interview{ID: d.ID(), ApplicationID: a.ID, Round: in.Round, ScheduledAt: in.ScheduledAt.UTC(), Notes: in.Note}
			if in.Kind == "RESCHEDULE" {
				previous, e := One[d.Interview](ctx, tx, "SELECT body FROM interviews WHERE id=? AND user_id=? AND application_id=? FOR UPDATE", in.InterviewID, user, a.ID)
				if e != nil {
					return e
				}
				if previous.FinishedAt != nil {
					return ErrConflict
				}
				if !previous.ScheduledAt.Equal(*in.PreviousScheduledAt) {
					return ErrConflict
				}
				if previous.Round != in.Round {
					return ErrConflict
				}
				interview = previous
				interview.ScheduledAt = in.ScheduledAt.UTC()
				interview.Notes = in.Note
				_, e = tx.ExecContext(ctx, "UPDATE interviews SET body=? WHERE id=? AND user_id=?", d.JSON(interview), interview.ID, user)
				if e != nil {
					return e
				}
			} else {
				_, e = tx.ExecContext(ctx, "INSERT INTO interviews(id,user_id,application_id,body) VALUES(?,?,?,?)", interview.ID, user, a.ID, d.JSON(interview))
				if e != nil {
					return e
				}
			}
			v.InterviewID = interview.ID
		}
		if in.ScheduledAt != nil && !applicationEnded(a.State) {
			at := in.ScheduledAt.UTC()
			a.FollowUpAt = &at
			a.NextAction = "处理已确认的招聘通知"
		}
		if applicationEnded(a.State) {
			a.FollowUpAt = nil
			a.NextAction = ""
		}
		a.Version++
		a.UpdatedAt = now
		if _, e = tx.ExecContext(ctx, "UPDATE applications SET version=?,body=? WHERE id=? AND user_id=?", a.Version, d.JSON(a), a.ID, user); e != nil {
			return e
		}
		event := d.ApplicationEvent{ID: d.ID(), ApplicationID: a.ID, From: old, To: a.State, OccurredAt: now, Actor: user, Note: "确认招聘通知 " + v.ID + " [" + in.Kind + "] " + in.Note}
		if _, e = tx.ExecContext(ctx, "INSERT INTO application_events(id,application_id,body) VALUES(?,?,?)", event.ID, a.ID, d.JSON(event)); e != nil {
			return e
		}
		v.State = "CONFIRMED"
		v.Version++
		v.ApplicationID = a.ID
		v.Confirmed = &in
		v.ConfirmationHash = hash
		_, e = tx.ExecContext(ctx, "UPDATE recruitment_notices SET application_id=?,state=?,version=?,body=? WHERE id=? AND user_id=?", a.ID, v.State, v.Version, d.JSON(v), v.ID, user)
		out = v
		return e
	})
	return out, err
}
