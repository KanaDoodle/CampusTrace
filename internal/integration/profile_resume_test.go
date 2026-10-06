package integration

import (
	"errors"
	"strings"
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
	bullet := "使用 Go 和 Redis 实现异步处理，\n通过失败重试提高可靠性。"
	project, err := store.SaveProject(ctx, owner, d.Project{Name: "任务队列", Description: "个人任务处理项目", Bullets: []string{bullet}})
	must(t, err)
	if _, err := store.UpdateProject(ctx, other, project.ID, d.Project{Name: "伪造名称"}); !errors.Is(err, p.ErrNotFound) {
		t.Fatalf("cross-owner project rename: %v", err)
	}
	renamed, err := store.UpdateProject(ctx, owner, project.ID, d.Project{Name: "任务队列服务"})
	must(t, err)
	if renamed.ID != project.ID || renamed.Name != "任务队列服务" || renamed.Description != project.Description || len(renamed.Bullets) != 1 || renamed.Bullets[0] != bullet {
		t.Fatalf("renamed project: %+v", renamed)
	}
	cleared, err := store.UpdateProject(ctx, owner, project.ID, d.Project{Name: renamed.Name, Bullets: []string{}})
	must(t, err)
	if cleared.Description != "" || len(cleared.Bullets) != 0 {
		t.Fatal("explicitly cleared project content was retained", cleared)
	}
	for _, incoming := range []d.Project{
		{Name: "任务队列", Description: "联系 test-private@example.com"},
		{Name: "任务队列", Bullets: []string{"联系电话：13812345678"}},
		{Name: "任务队列", Bullets: []string{strings.Repeat("后", 667)}},
	} {
		if _, err := store.SaveProject(ctx, owner, incoming); !errors.Is(err, p.ErrValidation) {
			t.Fatal("invalid or identifying project content was stored", err)
		}
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
	longBullet := strings.Repeat("通过重试恢复任务。", 45)
	full, err := store.SaveFact(ctx, owner, d.ProjectFact{ProjectID: project.ID, Kind: "IMPLEMENTED", Claim: longBullet, Reference: longBullet, Verified: true})
	must(t, err)
	if full.Claim != longBullet || full.Reference != longBullet {
		t.Fatal("long complete bullet was truncated", full)
	}
}
