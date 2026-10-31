package persistence

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"testing"
	"time"
)

func TestCompanyPlanRespectsEachBatchUnknownRulesAndDerivedClosure(t *testing.T) {
	s := MatchSnapshot{CandidateHash: "synthetic", CompanyWorkflow: &CompanyWorkflow{AsOf: time.Now(), Campaigns: []CampaignView{{ApplicationCampaign: ApplicationCampaign{ID: "batch", Limit: 2, JobIDs: []string{"a", "b", "c", "closed"}}, Planned: 1, Remaining: 1}}, Jobs: []DecisionJobContext{{JobID: "closed", CurrentStatus: "CLOSED"}}}}
	for _, id := range []string{"a", "b", "c", "unknown", "closed"} {
		s.Jobs = append(s.Jobs, MatchJob{Job: d.Job{ID: id, CurrentStatus: "OPEN"}, InputKey: id})
	}
	s.Jobs[2].Application = &MatchApplication{ID: "existing", State: "PLANNED"}
	report := matching.CompanyComparison{Holistic: &matching.HolisticCompanyReport{Choices: []matching.CompanyChoice{{ID: "closed", Rank: 1}, {ID: "unknown", Rank: 2}, {ID: "a", Rank: 3}, {ID: "b", Rank: 4}, {ID: "c", Rank: 5}}}}
	p := BuildCompanyDecisionPlan(s, report)
	if len(p.Suggested) != 1 || p.Suggested[0] != "a" || len(p.UnknownRules) != 1 || p.UnknownRules[0] != "unknown" || p.Blocked["c"] == "" || p.Blocked["closed"] == "" {
		t.Fatal(p)
	}
	s.CompanyWorkflow.AsOf = s.CompanyWorkflow.AsOf.Add(time.Minute)
	if BuildCompanyDecisionPlan(s, report).Key != p.Key {
		t.Fatal("display clock invalidates every plan")
	}
	s.CompanyWorkflow.Campaigns[0].Remaining = 0
	if BuildCompanyDecisionPlan(s, report).Key == p.Key {
		t.Fatal("quota change kept stale plan")
	}
}

func TestAdversarialCompanyPlanHardGateAndMissingQualifications(t *testing.T) {
	snapshot := MatchSnapshot{CandidateHash: "synthetic", CompanyWorkflow: &CompanyWorkflow{Campaigns: []CampaignView{{ApplicationCampaign: ApplicationCampaign{ID: "batch", JobIDs: []string{"blocked", "unknown"}, Limit: 1, Confirmed: true}, Remaining: 1}}}}
	for _, id := range []string{"blocked", "unknown"} {
		snapshot.Jobs = append(snapshot.Jobs, MatchJob{Job: d.Job{ID: id, CurrentStatus: "OPEN"}, InputKey: id, State: "ANALYZED", Result: &matching.Result{Qualifications: d.Eligibility{Status: map[string]string{"blocked": "INELIGIBLE", "unknown": "UNKNOWN"}[id]}}})
	}
	report := matching.CompanyComparison{Holistic: &matching.HolisticCompanyReport{Choices: []matching.CompanyChoice{{ID: "blocked", Rank: 1}, {ID: "unknown", Rank: 2}}}}
	plan := BuildCompanyDecisionPlan(snapshot, report)
	if plan.Blocked["blocked"] == "" || plan.Blocked["unknown"] != "" || len(plan.Suggested) != 1 || plan.Suggested[0] != "unknown" {
		t.Fatal("unknown treated as failure, or explicit failure ignored", plan)
	}
}
