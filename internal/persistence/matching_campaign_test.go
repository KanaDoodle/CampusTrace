package persistence

import (
	"testing"
	"time"
)

func TestCampaignHidingUsesActualSubmissionsAndPreservesActiveRecords(t *testing.T) {
	at := time.Now()
	apps := map[string]*MatchApplication{
		"applied":   {AppliedAt: &at, State: "REJECTED"},
		"planned":   {State: "PLANNED"},
		"withdrawn": {State: "WITHDRAWN"},
	}
	for _, limit := range []int{1, 2, 3} {
		rule := CampaignView{ApplicationCampaign: ApplicationCampaign{ID: "batch", Name: "已确认批次", Confirmed: true, Limit: limit, JobIDs: []string{"applied", "planned", "withdrawn", "unhandled"}}, Planned: limit, Submitted: limit - 1}
		before := matchCampaignHints([]CampaignView{rule}, apps)
		for _, v := range before {
			if v.HideUnsubmitted {
				t.Fatal("planned reservations hid browsing jobs before actual submission")
			}
		}
		rule.Submitted = limit
		full := matchCampaignHints([]CampaignView{rule}, apps)
		if full["applied"].HideUnsubmitted || full["planned"].HideUnsubmitted || !full["withdrawn"].HideUnsubmitted || !full["unhandled"].HideUnsubmitted {
			t.Fatal("full batch lost a real submission/active plan or kept unsubmitted candidates", full)
		}
		if full["outside"] != nil || full["unhandled"].Submitted != limit || full["unhandled"].ID != "batch" {
			t.Fatal("hiding crossed the confirmed job membership")
		}
		rule.Confirmed = false
		if len(matchCampaignHints([]CampaignView{rule}, apps)) != 0 {
			t.Fatal("unconfirmed rule changed browsing")
		}
	}
}
