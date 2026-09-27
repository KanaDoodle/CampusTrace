package integration

import (
	"errors"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func TestProfileFactReviewAndOwnerScope(t *testing.T) {
	ctx, store, _, owner, _ := setup(t)
	other, err := store.NewUser(ctx, d.ID()+"@synthetic.test", "unused-test-hash")
	must(t, err)
	must(t, store.SaveProfile(ctx, owner, d.Profile{GraduationYear: 2027, Skills: []string{"Go"}}))
	first, err := store.Profile(ctx, owner)
	must(t, err)
	must(t, store.SaveProfile(ctx, owner, d.Profile{Revision: first.Revision, GraduationYear: 2027, Skills: []string{"Go", "Redis"}}))
	if err := store.SaveProfile(ctx, owner, d.Profile{Revision: first.Revision, Skills: []string{"Java"}}); !errors.Is(err, p.ErrConflict) {
		t.Fatalf("stale profile write: %v", err)
	}
	project, err := store.SaveProject(ctx, owner, d.Project{Name: "任务队列"})
	must(t, err)
	if _, err := store.UpdateProject(ctx, other, project.ID, d.Project{Name: "伪造名称"}); !errors.Is(err, p.ErrNotFound) {
		t.Fatalf("cross-owner project rename: %v", err)
	}
	renamed, err := store.UpdateProject(ctx, owner, project.ID, d.Project{Name: "任务队列服务"})
	must(t, err)
	if renamed.ID != project.ID || renamed.Name != "任务队列服务" {
		t.Fatalf("renamed project: %+v", renamed)
	}
	fact, err := store.SaveFact(ctx, owner, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: "实现失败重试", Reference: "使用 Go 实现失败重试", Verified: false})
	must(t, err)
	if _, err := store.UpdateFact(ctx, other, fact.ID, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: "伪造", Verified: true}); !errors.Is(err, p.ErrNotFound) {
		t.Fatalf("cross-owner fact edit: %v", err)
	}
	updated, err := store.UpdateFact(ctx, owner, fact.ID, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: "实现失败重试和幂等", Reference: fact.Reference, Verified: true})
	must(t, err)
	if !updated.Verified || updated.ID != fact.ID || !updated.CreatedAt.Equal(fact.CreatedAt) || updated.Claim != "实现失败重试和幂等" {
		t.Fatalf("updated fact: %+v", updated)
	}
	if _, err := store.UpdateFact(ctx, owner, fact.ID, d.ProjectFact{ProjectID: d.ID(), Kind: "IMPLEMENTED", Claim: "移动项目", Verified: true}); !errors.Is(err, p.ErrValidation) {
		t.Fatalf("cross-project move: %v", err)
	}
}
