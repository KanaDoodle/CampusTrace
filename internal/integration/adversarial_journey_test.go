package integration

import (
	"context"
	"encoding/json"
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"sync"
	"testing"
)

func TestAdversarialDifferentCompanySelectionsRaceForLastSlot(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, u, _, ids, _, _, _ := wholeSetup(t)
	job, e := s.Job(ctx, ids[0])
	must(t, e)
	_, e = s.SaveCampaign(ctx, u, "", p.ApplicationCampaign{CompanyID: job.CompanyID, Name: "Synthetic one slot", Limit: 1, JobIDs: ids, Confirmed: true})
	must(t, e)
	snapshot, e := s.MatchWorkspaceSnapshot(ctx, u, "offline", "", ids, job.Company)
	must(t, e)
	plan := p.BuildCompanyDecisionPlan(snapshot, matching.CompanyComparison{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	gate := make(chan struct{})
	for _, id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-gate
			_, e := s.ConfirmCompanyDecision(ctx, u, "offline", "", job.Company, ids, []string{id}, plan.Key)
			results <- e
		}(id)
	}
	close(gate)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, p.ErrConflict) || errors.Is(e, p.ErrCampaignLimit) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	apps, e := s.ApplicationRecords(ctx, u)
	must(t, e)
	if success != 1 || conflict != 1 || len(apps) != 1 {
		t.Fatal(success, conflict, len(apps))
	}
	// A reviewed snapshot may not silently absorb a subsequent JD edit.
	a := apps[0]
	preview, e := s.ApplicationContextPreview(ctx, u, a.ID, "synthetic reviewed resume")
	must(t, e)
	j, e := s.Job(ctx, a.JobID)
	must(t, e)
	j.Title += " changed"
	_, e = s.DB.ExecContext(ctx, "UPDATE jobs SET body=? WHERE id=?", d.JSON(j), j.ID)
	must(t, e)
	_, e = s.ApplyAction(ctx, u, d.ID(), "transition_application", []byte(d.JSON(p.TransitionArgs{ApplicationID: a.ID, Version: a.Version, State: "APPLIED", ContextKey: preview.Key, ResumeText: "synthetic reviewed resume"})))
	if !errors.Is(e, p.ErrConflict) {
		t.Fatal("old preview accepted changed JD", e)
	}
}

func TestAdversarialNoticeReferenceConflictAndNoUnconfirmedMutation(t *testing.T) {
	adversarialEnabled(t)
	ctx, s, u, _, ids, _, _, _ := wholeSetup(t)
	apps := []d.Application{}
	for i, id := range ids {
		raw, e := s.ApplyAction(ctx, u, d.ID(), "record_submitted_application", []byte(d.JSON(p.CreateArgs{JobID: id})))
		must(t, e)
		var a d.Application
		must(t, json.Unmarshal(raw, &a))
		reference := []string{"SYN-A1", "SYN-B2"}[i]
		notice, e := s.ImportRecruitmentNotice(ctx, u, "Whole Synthetic application id: "+reference+" archive only", "text")
		must(t, e)
		_, e = s.ConfirmRecruitmentNotice(ctx, u, notice.Notice.ID, p.NoticeConfirmation{Version: 1, ApplicationID: a.ID, ApplicationVersion: a.Version, Kind: "UNKNOWN", Reference: reference})
		must(t, e)
		rec, e := s.ApplicationRecord(ctx, u, a.ID)
		must(t, e)
		apps = append(apps, rec.Application)
	}
	view, e := s.ImportRecruitmentNotice(ctx, u, "Whole Synthetic application id: SYN-A1\n面试时间 2026-11-03 14:00 UTC。", "text")
	must(t, e)
	if len(view.Candidates) != 2 || !view.Ambiguous || view.Candidates[0].Application.ID != apps[0].ID {
		t.Fatal("reference evidence or ambiguity lost", view)
	}
	for _, a := range apps {
		current, e := s.ApplicationRecord(ctx, u, a.ID)
		must(t, e)
		if current.State != "APPLIED" || current.Version != a.Version {
			t.Fatal("import mutated application", current)
		}
	}
	wrong := p.NoticeConfirmation{Version: 1, ApplicationID: apps[1].ID, ApplicationVersion: apps[1].Version, Kind: "INTERVIEW", ScheduledAt: view.Notice.Parsed.ScheduledAt, Round: 1, Reference: "SYN-A1"}
	if _, e = s.ConfirmRecruitmentNotice(ctx, u, view.Notice.ID, wrong); !errors.Is(e, p.ErrConflict) {
		t.Fatal("conflicting reference linked", e)
	}
	other, e := s.NewUser(ctx, d.ID()+"@synthetic.test", "unused")
	must(t, e)
	t.Cleanup(func() { cleanupFixture(context.Background(), s, other, "") })
	if _, e = s.ConfirmRecruitmentNotice(ctx, other, view.Notice.ID, wrong); !errors.Is(e, p.ErrNotFound) {
		t.Fatal("cross-account notification linked", e)
	}
	correct := wrong
	correct.ApplicationID = apps[0].ID
	correct.ApplicationVersion = apps[0].Version
	_, e = s.ConfirmRecruitmentNotice(ctx, u, view.Notice.ID, correct)
	must(t, e)
	after, e := s.ApplicationDossier(ctx, u, apps[0].ID)
	must(t, e)
	if len(after.Interviews) != 1 || after.Interviews[0].ScheduledAt.Hour() != 14 {
		t.Fatal("UTC confirmation lost exact time", after.Interviews)
	}
}
