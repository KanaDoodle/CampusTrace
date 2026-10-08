package domain

import "testing"

func TestFoundationAssessmentsAreExplicitAndCanonical(t *testing.T) {
	p := Profile{}
	if p.NormalizeFoundationSkills() != nil || p.FoundationSkills != nil {
		t.Fatal("omission lost")
	}
	p.FoundationSkills = []FoundationSkill{{"COMPUTER_NETWORKS", "PRACTICED"}, {"ALGORITHMS", "UNDERSTAND"}}
	if p.NormalizeFoundationSkills() != nil || p.FoundationSkills[0].Topic != "ALGORITHMS" {
		t.Fatal(p)
	}
	for _, skills := range [][]FoundationSkill{
		{{"ALGORITHMS", "EXPERT"}}, {{"OTHER", "FAMILIAR"}}, {{"ALGORITHMS", ""}}, {{"ALGORITHMS", "UNDERSTAND"}, {"ALGORITHMS", "PRACTICED"}},
	} {
		p.FoundationSkills = skills
		if p.NormalizeFoundationSkills() == nil {
			t.Fatal("invalid self-assessment accepted", skills)
		}
	}
	p.FoundationSkills = []FoundationSkill{}
	if p.NormalizeFoundationSkills() != nil || p.FoundationSkills == nil {
		t.Fatal("explicit clearing lost")
	}
}
