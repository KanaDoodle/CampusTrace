package resume

import "testing"

func TestResumeKeepsBothEducationsAndRejectsUnboundEducation(t *testing.T) {
	text := "本科：计算机，2020-2024，已毕业。\n硕士：数学，2024-2027，在读。"
	items := []Education{{Degree: "BACHELOR", Majors: []string{"计算机"}, StartYear: 2020, GraduationYear: 2024, Status: "GRADUATED", Excerpt: "本科：计算机，2020-2024，已毕业。"}, {Degree: "MASTER", Majors: []string{"数学"}, StartYear: 2024, GraduationYear: 2027, Status: "ENROLLED", Excerpt: "硕士：数学，2024-2027，在读。"}, {Degree: "PHD", Majors: []string{"数学"}, Status: "UNKNOWN", Excerpt: "博士：数学"}}
	draft, err := analyzeDraft(t, text, Draft{Educations: items, Suggestions: []Suggestion{{Field: "degree", Value: "BACHELOR", Excerpt: "本科"}, {Field: "degree", Value: "MASTER", Excerpt: "硕士"}}})
	if err != nil || len(draft.Educations) != 2 || len(draft.Suggestions) != 0 || len(draft.Warnings) != 1 || draft.Warnings[0].Scope != "EDUCATION" || draft.Warnings[0].ItemIndex != 3 {
		t.Fatalf("%+v %v", draft, err)
	}
	if draft.Educations[0].GraduationYear != 2024 || draft.Educations[1].GraduationYear != 2027 || draft.Educations[1].Status != "ENROLLED" {
		t.Fatal(draft)
	}
}

func TestConflictingLegacyScalarSuggestionsCannotOverwriteEachOther(t *testing.T) {
	draft, err := analyzeDraft(t, "本科、硕士，熟悉 Go", Draft{Suggestions: []Suggestion{{Field: "degree", Value: "BACHELOR", Excerpt: "本科"}, {Field: "degree", Value: "MASTER", Excerpt: "硕士"}, {Field: "target_languages", Value: "Go", Excerpt: "Go"}}})
	if err != nil || len(draft.Suggestions) != 1 || len(draft.Warnings) != 2 || draft.Warnings[0].Reason != "VALUE_CONFLICT" {
		t.Fatalf("%+v %v", draft, err)
	}
}
