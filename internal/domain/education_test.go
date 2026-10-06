package domain

import "testing"

func TestGraduationMonthIsBoundToSelectedEducationAndOptional(t *testing.T) {
	p := Profile{Educations: []Education{{ID: "b", Degree: "BACHELOR", GraduationYear: 2023, GraduationMonth: 6, Status: "GRADUATED"}, {ID: "m", Degree: "MASTER", GraduationYear: 2027, GraduationMonth: 0, Status: "ENROLLED"}}, PrimaryEducationID: "m"}
	if err := p.NormalizeEducations(); err != nil || p.GraduationMonth != 0 {
		t.Fatal(p, err)
	}
	p.PrimaryEducationID = "b"
	p = p.EducationProfile()
	if p.GraduationYear != 2023 || p.GraduationMonth != 6 {
		t.Fatal(p)
	}
	for _, e := range []Education{{Status: "UNKNOWN", GraduationMonth: 6}, {Status: "UNKNOWN", GraduationYear: 2027, GraduationMonth: 13}, {Status: "UNKNOWN", GraduationYear: 2027, GraduationMonth: -1}} {
		if e.Validate() == nil {
			t.Fatal("invalid month accepted", e)
		}
	}
}

func TestEducationProjectionKeepsDegreesYearsAndMajorsBound(t *testing.T) {
	p := Profile{Degree: "BACHELOR", GraduationYear: 2024, Educations: []Education{{ID: "b", Degree: "BACHELOR", Majors: []string{"计算机"}, StartYear: 2020, GraduationYear: 2024, Status: "GRADUATED"}, {ID: "m", Degree: "MASTER", Majors: []string{"数学"}, StartYear: 2024, GraduationYear: 2027, Status: "ENROLLED"}}}
	if err := p.NormalizeEducations(); err != nil {
		t.Fatal(err)
	}
	if p.PrimaryEducationID != "m" || p.Degree != "MASTER" || p.GraduationYear != 2027 || len(p.Majors) != 2 {
		t.Fatal(p)
	}
	if got := p.MajorsForRequirement("本科专业须为数学"); len(got) != 1 || got[0] != "计算机" {
		t.Fatal(got)
	}
	if got := p.MajorsForRequirement("本科及以上学历，数学相关专业"); len(got) != 2 {
		t.Fatal(got)
	}
	p.PrimaryEducationID = "b"
	p = p.EducationProfile()
	if p.Degree != "BACHELOR" || p.GraduationYear != 2024 {
		t.Fatal("cross-degree date", p)
	}
	legacy := Profile{Degree: "MASTER", GraduationYear: 2027, Majors: []string{"数学"}}
	if legacy.EducationProfile().Degree != "MASTER" || legacy.EducationProfile().GraduationYear != 2027 {
		t.Fatal("legacy erased")
	}
}

func TestEducationValidationRejectsInvalidSelectionAndDates(t *testing.T) {
	e := Education{ID: "e", Degree: "MASTER", Majors: []string{"数学"}, Status: "ENROLLED", StartYear: 2024, GraduationYear: 2027}
	for _, p := range []Profile{{Educations: []Education{e, e}}, {Educations: []Education{e}, PrimaryEducationID: "missing"}} {
		if p.NormalizeEducations() == nil {
			t.Fatal("invalid history accepted", p)
		}
	}
	e.StartYear = 2028
	if e.Validate() == nil {
		t.Fatal("reversed dates accepted")
	}
	e.StartYear = 2024
	e.Status = "finished"
	if e.Validate() == nil {
		t.Fatal("unknown status accepted")
	}
}
