package persistence

import (
	"context"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type ApplicationFollowUp struct {
	Version    int        `json:"version"`
	FollowUpAt *time.Time `json:"follow_up_at"`
	NextAction string     `json:"next_action"`
}

func (s *Store) SaveApplicationFollowUp(ctx context.Context, user, id string, in ApplicationFollowUp) (d.Application, error) {
	in.NextAction = strings.TrimSpace(in.NextAction)
	if len(in.NextAction) > 600 || (in.FollowUpAt == nil && in.NextAction != "") {
		return d.Application{}, ErrValidation
	}
	if in.FollowUpAt != nil {
		year := in.FollowUpAt.In(time.FixedZone("Asia/Shanghai", 8*3600)).Year()
		if year < 2000 || year > 2100 {
			return d.Application{}, ErrValidation
		}
	}
	return s.updateApplicationRecord(ctx, user, id, in.Version, func(v *d.Application) (string, error) {
		if in.FollowUpAt == nil {
			v.FollowUpAt, v.NextAction = nil, ""
			return "取消跟进提醒", nil
		}
		if applicationEnded(v.State) {
			return "", ErrValidation
		}
		at := in.FollowUpAt.UTC()
		v.FollowUpAt, v.NextAction = &at, in.NextAction
		return "设置跟进提醒：" + at.Format(time.RFC3339) + "；" + in.NextAction, nil
	})
}

func (s *Store) CompleteApplicationFollowUp(ctx context.Context, user, id string, version int) (d.Application, error) {
	return s.updateApplicationRecord(ctx, user, id, version, func(v *d.Application) (string, error) {
		if v.FollowUpAt == nil {
			return "", ErrConflict
		}
		note := "已完成跟进"
		if v.NextAction != "" {
			note += "：" + v.NextAction
		}
		v.FollowUpAt, v.NextAction = nil, ""
		return note, nil
	})
}

func applicationEnded(state string) bool {
	return state == "OFFER" || state == "REJECTED" || state == "WITHDRAWN"
}
