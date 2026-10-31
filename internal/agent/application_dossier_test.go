package agent

import (
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"strings"
	"testing"
)

func TestApplicationDossierToolKeepsLegacyUnknownAndFrozenProvenance(t *testing.T) {
	id := strings.Repeat("a", 32)
	if Validate("get_application_dossier", []byte(`{"application_id":"`+id+`"}`)) != nil || Validate("get_application_dossier", []byte(`{"application_id":"`+id+`","user_id":"forged"}`)) == nil || IsWrite("get_application_dossier") {
		t.Fatal("tool authority")
	}
	legacy := p.ApplicationDossier{Legacy: true, Application: p.ApplicationRecord{Application: d.Application{ID: id}}}
	data := applicationDossierObservation(legacy, &Tools{})
	answer := dossierAnswer(json.RawMessage(d.JSON(data)))
	if len(answer) != 1 || !strings.Contains(answer[0], "没有历史材料快照") {
		t.Fatal(answer)
	}
	v := legacy
	v.Legacy = false
	v.Context = &p.ApplicationContext{Key: "frozen-key", Phase: "SUBMITTED", JD: "留档岗位原文", CandidateDocument: "当时材料，不替换为当前材料", ResumeText: "private@example.invalid", Job: d.Job{Title: "留档岗位"}}
	data = applicationDossierObservation(v, &Tools{})
	if data["context_key"] != "frozen-key" || data["candidate_document"] != v.Context.CandidateDocument || strings.Contains(d.JSON(data), "private@example.invalid") {
		t.Fatal(data)
	}
	facts := []any{map[string]any{"tool": "get_application_dossier", "data": data}}
	actions := NextActions(facts)
	if len(actions) != 2 || actions[0].Kind != "dossier" || actions[0].ApplicationID != id || actions[1].Kind != "application_preparation" {
		t.Fatal(actions)
	}
	if text := GroundedAnswer(facts); !strings.Contains(text, "frozen-key") || strings.Contains(text, "QPS") {
		t.Fatal(text)
	}
}
