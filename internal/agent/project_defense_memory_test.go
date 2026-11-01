package agent

import (
	"encoding/json"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestProjectDefenseConfirmedExperienceReachesPrompt(t *testing.T) {
	s, _ := defenseFixture(t, "go-langgraph")
	// The same JSON shape is persisted with a reviewed cross-practice snapshot.
	raw := d.JSON(map[string]any{"memories": []map[string]any{{"id": "confirmed-experience", "note": "上次遗漏了由Go发送模型请求", "episode": map[string]any{"topic": "go-langgraph"}}}})
	if err := json.Unmarshal([]byte(raw), s.Defense); err != nil {
		t.Fatal(err)
	}
	messages := defenseMessages(s.Input, s.Sources, s.Missing, s.Defense)
	if !strings.Contains(messages[1].Content, "confirmed-experience") {
		t.Fatal("confirmed cross-practice experience is absent from the model context")
	}
}

func TestProjectDefenseMemoryOffAndEvidenceNeverBecomesCurrentRead(t *testing.T) {
	s, r := defenseFixture(t, "go-langgraph")
	s.Defense.Memories = []d.DefenseMemory{{ID: "confirmed-experience", Note: "旧反馈只作线索", Episode: &d.DefenseEpisode{Sources: s.Defense.Evidence, Review: r}}}
	if validateDefenseReview(s, r) == nil {
		t.Fatal("recalled evidence bypassed current read tool")
	}
	s.Input.Defense.MemoryMode = "off"
	if strings.Contains(defenseMessages(s.Input, s.Sources, s.Missing, s.Defense)[1].Content, "confirmed-experience") {
		t.Fatal("off mode leaked memory")
	}
	s.Input.Defense.MemoryMode = "all-users"
	if ValidatePreparationInput(s.Input) == nil {
		t.Fatal("unsupported memory scope accepted")
	}
}
