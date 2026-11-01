package persistence

import (
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestDefenseRecallRejectsExpiredDisputedOtherTopicAndOldEvidence(t *testing.T) {
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	makeMemory := func(id string) AgentMemory {
		return AgentMemory{MemoryInput: MemoryInput{ID: id, Version: 1, Kind: "DEFENSE", Content: "用户纠正：上次只是没展开，不代表不会", Episode: &d.DefenseEpisode{Topic: "go-langgraph", EvidenceVersion: "current"}}, ExpiresAt: now.Add(time.Hour), UpdatedAt: now}
	}
	active := makeMemory("active")
	stale := makeMemory("stale")
	stale.Episode.EvidenceVersion = "old"
	expired := makeMemory("expired")
	expired.ExpiresAt = now
	disputed := makeMemory("disputed")
	disputed.Disputed = true
	other := makeMemory("other")
	other.Episode.Topic = "unknown-recovery"
	rows, excluded := SelectDefenseMemories([]AgentMemory{stale, expired, disputed, other, active}, "go-langgraph", "current", now)
	if len(rows) != 1 || rows[0].ID != "active" || excluded != 3 || rows[0].Note != active.Content {
		t.Fatal(rows, excluded)
	}
	active.Content = "corrected"
	active.Version = 2
	rows, _ = SelectDefenseMemories([]AgentMemory{active}, "go-langgraph", "current", now)
	if rows[0].Version != 2 || rows[0].Note != "corrected" {
		t.Fatal(rows)
	}
}

func TestDefenseRecallHasCountAndByteBudgets(t *testing.T) {
	now := time.Now()
	rows := []AgentMemory{}
	for _, id := range []string{"d", "c", "b", "a"} {
		rows = append(rows, AgentMemory{MemoryInput: MemoryInput{ID: id, Kind: "DEFENSE", Episode: &d.DefenseEpisode{Topic: "go-langgraph", EvidenceVersion: "v"}}, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)})
	}
	selected, excluded := SelectDefenseMemories(rows, "go-langgraph", "v", now)
	if len(selected) != 3 || selected[0].ID != "a" || excluded != 1 {
		t.Fatal(selected, excluded)
	}
	rows[0].Episode.Answer = strings.Repeat("x", 24577)
	selected, excluded = SelectDefenseMemories(rows[:1], "go-langgraph", "v", now)
	if len(selected) != 0 || excluded != 1 {
		t.Fatal(selected, excluded)
	}
}

func TestDefenseMemoryCannotBeForgedThroughOrdinaryWrite(t *testing.T) {
	in := MemoryInput{Kind: "DEFENSE", Content: "已经完全掌握", Days: 30, Episode: &d.DefenseEpisode{Topic: "go-langgraph"}}
	if ValidateAction("remember_memory", []byte(d.JSON(in))) == nil {
		t.Fatal("ordinary model memory tool can forge practice evidence")
	}
	in.Kind = "PREFERENCE"
	if in.Validate() == nil {
		t.Fatal("episode disguised as preference")
	}
	a := SaveDefenseMemoryArgs{ExecutionID: strings.Repeat("a", 32), Revision: 1, ReviewHash: strings.Repeat("b", 64), EvidenceVersion: strings.Repeat("c", 64), Days: 90}
	if ValidateAction("remember_defense_memory", []byte(d.JSON(a))) != nil {
		t.Fatal("valid bounded proposal rejected")
	}
	if ValidateAction("remember_defense_memory", []byte(strings.TrimSuffix(d.JSON(a), "}")+`,"episode":{"answer":"forged"}}`)) == nil {
		t.Fatal("client-supplied episode accepted")
	}
	a.Days = 366
	if a.Validate() == nil {
		t.Fatal("unbounded retention")
	}
}
