package analysis

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/rules"
	"testing"
	"time"
)

func TestRepairCacheTextIdentity(t *testing.T) {
	if d.Hash("tech: go\napply: PRESENT") == d.Hash("tech: go apply: PRESENT") {
		t.Fatal("F03: line boundaries lost")
	}
}
func TestRepairNegativeEvidence(t *testing.T) {
	for _, text := range []string{"Applications are closed. Do not submit.", "Do not apply now; hiring is paused.", "application closed", "applications ended", "position closed", "招聘已截止", "停止招聘", "暂停招聘", "申请已结束", "岗位已关闭", "暂不接受申请"} {
		t.Run(text, func(t *testing.T) {
			for _, c := range []d.Claim{{Type: "APPLY_ACTION", Value: "PRESENT", Excerpt: text, Method: "LLM", Confidence: 1}, {Type: "OPEN_SIGNAL", Value: "OPEN", Excerpt: text, Method: "LLM", Confidence: 1}} {
				if c.Validate(text) == nil {
					t.Error("F05: negative excerpt accepted as positive evidence")
				}
			}
			cs, err := Extract(context.Background(), text)
			if err != nil {
				t.Fatal(err)
			}
			if len(cs) == 0 {
				t.Error("F05: no closure signal")
			}
			for _, c := range cs {
				if c.Type == "APPLY_ACTION" && c.Value == "PRESENT" {
					t.Error("F05: negative text parsed as apply")
				}
			}
		})
	}
}
func TestRepairConflictingEvidence(t *testing.T) {
	text := "Apply now\nApplications are closed. Do not submit."
	cs, err := Extract(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	o := d.Observation{ID: "o", PostingID: "p", Text: text, FetchStatus: "SUCCESS", Trust: "OFFICIAL", ObservedAt: now}
	es := []d.Evidence{}
	for _, c := range cs {
		es = append(es, d.Evidence{ObservationID: "o", Claim: c})
	}
	if a := rules.Status("j", []d.Observation{o}, es, now); a.Status == "OPEN" {
		t.Fatal("F05: positive won conflicting signals")
	}
}
