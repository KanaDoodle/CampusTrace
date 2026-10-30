package persistence

import (
	"fmt"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

func autoRow(id, direction string) MatchJob {
	return MatchJob{Job: d.Job{ID: id, Company: "Company", Title: "岗位 " + id, CurrentStatus: "OPEN"}, Text: "使用Go开发服务，岗位 " + id, State: "BASIC", Local: &matching.LocalScreen{Direction: matching.Direction{Status: direction}}}
}
func TestAutoCompanyExcludesUnavailableWorkButKeepsUncertainDirection(t *testing.T) {
	rows := []MatchJob{autoRow("main", "MATCH"), autoRow("uncertain", "UNCERTAIN"), autoRow("closed", "MATCH"), autoRow("ignored", "MATCH"), autoRow("applied", "MATCH"), autoRow("quota", "MATCH"), autoRow("unrelated", "UNRELATED"), autoRow("missing", "MATCH"), autoRow("other-company", "MATCH")}
	rows[2].Job.CurrentStatus = "CLOSED"
	rows[3].Disposition = "IGNORED"
	rows[4].Application = &MatchApplication{State: "APPLIED"}
	rows[5].Campaign = &MatchCampaign{HideUnsubmitted: true}
	rows[7].Text = ""
	rows[8].Job.Company = "Other"
	got := autoCompanyCandidates(MatchSnapshot{Jobs: rows}, "Company", time.Now())
	if got.Total != 8 || got.Eligible != 2 || len(got.Selected) != 2 || got.Selected[0].JobID != "main" || got.Selected[1].JobID != "uncertain" {
		t.Fatal(got)
	}
	for _, key := range []string{"closed", "ignored", "applied", "quota", "unrelated", "unavailable"} {
		if got.Skipped[key] != 1 {
			t.Fatal(key, got)
		}
	}
	rows[4].Application = &MatchApplication{State: "PLANNED"}
	got = autoCompanyCandidates(MatchSnapshot{Jobs: rows}, "Company", time.Now())
	if got.Eligible != 3 {
		t.Fatal("unsubmitted plan excluded", got)
	}
}
func TestAutoCompanyUsesOnlyCurrentHolisticFitAndDeterministicOrder(t *testing.T) {
	current, stale, basic := autoRow("current", "UNRELATED"), autoRow("stale", "MATCH"), autoRow("basic", "MATCH")
	current.State = "ANALYZED"
	current.Holistic = &matching.HolisticAssessment{Fit: "STRONG"}
	stale.State = "STALE"
	stale.Holistic = &matching.HolisticAssessment{Fit: "STRONG"}
	got := autoCompanyCandidates(MatchSnapshot{Jobs: []MatchJob{stale, basic, current}}, "Company", time.Now())
	if len(got.Selected) != 3 || got.Selected[0].JobID != "current" || got.Selected[1].JobID != "basic" {
		t.Fatal(got)
	}
	next := autoCompanyCandidates(MatchSnapshot{Jobs: []MatchJob{current, basic, stale}}, "Company", time.Now())
	for i := range got.Selected {
		if got.Selected[i].JobID != next.Selected[i].JobID {
			t.Fatal("order depends on catalog position")
		}
	}
}
func TestAutoCompanyDiversifiesIdenticalJDsBeforeFillingSixteenSlots(t *testing.T) {
	rows := []MatchJob{}
	for i := 0; i < 25; i++ {
		r := autoRow(fmt.Sprintf("a-%02d", i), "MATCH")
		r.Text = "同一份Go服务端JD"
		rows = append(rows, r)
	}
	for i := 0; i < 20; i++ {
		rows = append(rows, autoRow(fmt.Sprintf("b-%02d", i), "MATCH"))
	}
	got := autoCompanyCandidates(MatchSnapshot{Jobs: rows}, "Company", time.Now())
	duplicates := 0
	for _, r := range got.Selected {
		if strings.HasPrefix(r.JobID, "a-") {
			duplicates++
		}
	}
	if len(got.Selected) != 16 || duplicates != 1 || got.Deferred != 29 || got.Total != 45 {
		t.Fatal(got)
	}
}
func TestAutoCompanyPacksWholeInputWithoutTruncation(t *testing.T) {
	rows := []MatchJob{}
	for i := 0; i < 16; i++ {
		r := autoRow(fmt.Sprintf("job-%02d", i), "MATCH")
		r.Text = strings.Repeat("a", 23000) + r.Job.ID
		rows = append(rows, r)
	}
	long := autoRow("too-long", "MATCH")
	long.Text = strings.Repeat("a", matching.MaxBatchText+1)
	rows = append(rows, long)
	snapshot := MatchSnapshot{Jobs: rows}
	got := autoCompanyCandidates(snapshot, "Company", time.Now())
	if len(got.Selected) == 0 || len(got.Selected) >= 16 || got.Skipped["text_limit"] != 1 || got.Deferred != 16-len(got.Selected) {
		t.Fatal(got)
	}
	jobs := []MatchJob{}
	for _, chosen := range got.Selected {
		for _, row := range rows {
			if chosen.JobID == row.Job.ID {
				jobs = append(jobs, row)
			}
		}
	}
	if err := matching.ValidateHolisticInput(snapshot.Candidate, WholeJobs(jobs), true); err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if len(job.Text) != 23006 {
			t.Fatal("JD truncated", len(job.Text))
		}
	}
}
