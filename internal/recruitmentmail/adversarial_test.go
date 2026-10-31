package recruitmentmail

import (
	"testing"
	"time"
)

func TestAdversarialExplicitUTCIsNotDefaultedToShanghai(t *testing.T) {
	for _, zone := range []string{"UTC", "GMT"} {
		p, e := Parse("面试通知：请于 2026-11-03 14:00 "+zone+" 参加面试。", "text")
		if e != nil || p.ScheduledAt == nil || !p.ScheduledAt.Equal(time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC)) {
			t.Errorf("zone=%s scheduled=%v err=%v", zone, p.ScheduledAt, e)
		}
	}
}
func TestAdversarialReplyDeadlineIsNotInterviewAppointment(t *testing.T) {
	p, e := Parse("面试通知：请在 2026-11-03 12:00 前回复确认是否参加面试。面试时间另行通知。", "text")
	if e != nil {
		t.Fatal(e)
	}
	if p.ScheduledAt != nil {
		t.Fatal("reply deadline prefilled as interview", p.ScheduledAt)
	}
	if len(p.TimeCandidates) != 1 {
		t.Fatal("original candidate lost", p)
	}
}
func TestAdversarialUnknownNamedZoneRequiresConfirmation(t *testing.T) {
	p, e := Parse("面试通知：2026-11-03 14:00 CST 参加面试。", "text")
	if e != nil {
		t.Fatal(e)
	}
	if p.ScheduledAt != nil {
		t.Fatal("ambiguous zone silently defaulted", p.ScheduledAt)
	}
}
