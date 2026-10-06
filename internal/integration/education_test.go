package integration

import (
	"errors"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"testing"
)

func TestEducationHistoryPersistsWithCASAndSurvivesLegacyClientEdits(t *testing.T) {
	ctx, store, _, owner, _ := setup(t)
	profile := d.Profile{Educations: []d.Education{{ID: "b", Degree: "BACHELOR", Majors: []string{"计算机"}, StartYear: 2020, GraduationYear: 2024, Status: "GRADUATED"}, {ID: "m", Degree: "MASTER", Majors: []string{"数学"}, StartYear: 2024, GraduationYear: 2027, Status: "ENROLLED"}}, Languages: []string{"Go"}}
	must(t, store.SaveProfile(ctx, owner, profile))
	saved, err := store.Profile(ctx, owner)
	must(t, err)
	if len(saved.Educations) != 2 || saved.PrimaryEducationID != "m" || saved.Degree != "MASTER" || saved.GraduationYear != 2027 {
		t.Fatal(saved)
	}
	legacy := d.Profile{Revision: saved.Revision, Degree: "BACHELOR", GraduationYear: 2024, Skills: []string{"Redis"}}
	must(t, store.SaveProfile(ctx, owner, legacy))
	updated, err := store.Profile(ctx, owner)
	must(t, err)
	if len(updated.Educations) != 2 || updated.Degree != "MASTER" || updated.GraduationYear != 2027 || len(updated.Skills) != 1 {
		t.Fatal("history erased by old client", updated)
	}
	if err := store.SaveProfile(ctx, owner, saved); !errors.Is(err, p.ErrConflict) {
		t.Fatal("stale save accepted", err)
	}
	updated.PrimaryEducationID = "b"
	must(t, store.SaveProfile(ctx, owner, updated))
	selected, err := store.Profile(ctx, owner)
	must(t, err)
	if selected.Degree != "BACHELOR" || selected.GraduationYear != 2024 {
		t.Fatal("cross-bound projection", selected)
	}
	selected.PrimaryEducationID = "missing"
	if err := store.SaveProfile(ctx, owner, selected); !errors.Is(err, p.ErrValidation) {
		t.Fatal("invalid primary saved", err)
	}
	selected.PrimaryEducationID = "b"
	selected.Educations[0].Majors = []string{"contact test-private@example.com"}
	if err := store.SaveProfile(ctx, owner, selected); !errors.Is(err, p.ErrValidation) {
		t.Fatal("private education saved", err)
	}
	selected.Educations = []d.Education{}
	selected.PrimaryEducationID = ""
	selected.Degree = ""
	selected.GraduationYear = 0
	selected.Majors = nil
	must(t, store.SaveProfile(ctx, owner, selected))
	empty, err := store.Profile(ctx, owner)
	must(t, err)
	if len(empty.Educations) != 0 || empty.Degree != "" {
		t.Fatal("explicit clearing failed", empty)
	}
}
