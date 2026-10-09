package persistence

import (
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	"strings"
	"testing"
)

func TestDisplayCacheIsBoundedScopedAndCloned(t *testing.T) {
	c := inventoryViewCache{}
	v := displayInventory(MatchSnapshot{CandidateDocument: "PRIVATE", Candidate: matching.Candidate{Document: "PRIVATE", Facts: []matching.Fact{{Kind: "SKILL", Text: "PRIVATE"}, {Kind: "ROLE", Text: "后端"}}, Projects: []matching.CandidateProject{{Description: "PRIVATE"}}}, Jobs: []MatchJob{{Job: d.Job{ID: "a", OwnerID: "PRIVATE"}, Text: "PRIVATE"}}})
	c.put("alice", "1", v)
	if strings.Contains(string(c.entries[0].body), "PRIVATE") {
		t.Fatal("cache retained raw personal or job data")
	}
	one, ok := c.get("alice", "1")
	if !ok {
		t.Fatal("cache miss")
	}
	one.Jobs[0].Job.Title = "changed"
	two, _ := c.get("alice", "1")
	if two.Jobs[0].Job.Title != "" {
		t.Fatal("caller mutated shared cache")
	}
	if _, ok = c.get("bob", "1"); ok {
		t.Fatal("scope bypass")
	}
	for i := 0; i < 12; i++ {
		c.put("alice", fmt.Sprint(i+2), v)
	}
	if len(c.entries) > 8 || c.bytes > 64<<20 {
		t.Fatal("unbounded inventory cache")
	}
	if _, ok = c.get("alice", "1"); ok {
		t.Fatal("LRU retained evicted version")
	}
}
