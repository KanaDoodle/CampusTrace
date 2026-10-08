package integration

import (
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"testing"
)

func TestFoundationAssessmentPersistenceLegacyEditsAndClearing(t *testing.T) {
	ctx, store, _, owner, other := setup(t)
	profile := d.Profile{FoundationSkills: []d.FoundationSkill{{Topic: "COMPUTER_NETWORKS", Level: "FAMILIAR"}}}
	must(t, store.SaveProfile(ctx, owner, profile))
	saved, err := store.Profile(ctx, owner)
	must(t, err)
	legacy := d.Profile{Revision: saved.Revision, Languages: []string{"Go"}}
	must(t, store.SaveProfile(ctx, owner, legacy))
	updated, err := store.Profile(ctx, owner)
	must(t, err)
	if len(updated.FoundationSkills) != 1 || updated.FoundationSkills[0].Level != "FAMILIAR" {
		t.Fatal("omitted field erased", updated)
	}
	if err := store.SaveProfile(ctx, owner, saved); !errors.Is(err, p.ErrConflict) {
		t.Fatal("stale edit accepted", err)
	}
	if _, err := store.Profile(ctx, other); err == nil {
		t.Fatal("assessment leaked to another account")
	}
	updated.FoundationSkills = []d.FoundationSkill{}
	must(t, store.SaveProfile(ctx, owner, updated))
	cleared, err := store.Profile(ctx, owner)
	must(t, err)
	if len(cleared.FoundationSkills) != 0 {
		t.Fatal("explicit clear failed", cleared)
	}
	cleared.FoundationSkills = []d.FoundationSkill{{Topic: "ALGORITHMS", Level: "EXPERT"}}
	if err := store.SaveProfile(ctx, owner, cleared); !errors.Is(err, p.ErrValidation) {
		t.Fatal("invalid level accepted", err)
	}
}
