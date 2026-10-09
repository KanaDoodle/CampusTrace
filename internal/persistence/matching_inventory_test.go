package persistence

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

func inventoryFixture() MatchSnapshot {
	return MatchSnapshot{CandidateHash: "reviewed-v1", Profile: d.Profile{Languages: []string{"Go"}, TargetRoles: []string{"后端开发"}, Degree: "MASTER", GraduationYear: 2027}, Jobs: []MatchJob{
		{Job: d.Job{ID: "a", Company: "合成公司", Title: "服务端开发", Locations: []string{"上海"}, JobType: "FULL_TIME"}, Text: "2027届本科及以上，熟悉 Go 和 Redis。开发后端服务。", RequirementsKey: "jd-a", InputKey: "input-a"},
		{Job: d.Job{ID: "b", Company: "合成公司", Title: "前端开发", Locations: []string{"北京"}, JobType: "FULL_TIME"}, Text: "熟悉 JavaScript 与 React。开发前端页面。", RequirementsKey: "jd-b", InputKey: "input-b", ExcludedReason: "你已忽略这个岗位"},
	}}
}

func TestInventoryCacheBoundedIndependentAndSummaryOnly(t *testing.T) {
	c := inventoryScreenCache{maxEntries: 2, maxBytes: 1 << 20}
	value := matching.LocalScreen{Score: 70, Role: "后端", RoleExcerpt: "完整原文", Reasons: []string{"原因"}, Warnings: []string{"警告"}, Checks: []matching.LocalCheck{{Excerpt: "私人项目"}}, Direction: matching.Direction{Status: "MATCH", Roles: []string{"后端"}, Reason: "方向说明", Evidence: []matching.DirectionEvidence{{Excerpt: "岗位原文"}}}}
	c.put("a", value)
	value.Reasons[0], value.Direction.Roles[0] = "改写", "改写"
	got, ok := c.get("a")
	if !ok || got.Reasons[0] != "原因" || got.Direction.Roles[0] != "后端" || len(got.Checks) != 0 || got.RoleExcerpt != "" || len(got.Direction.Evidence) != 0 {
		t.Fatal("cache retained evidence or aliased caller slices", got)
	}
	got.Reasons[0], got.Warnings[0] = "改写", "改写"
	again, _ := c.get("a")
	if again.Reasons[0] != "原因" || again.Warnings[0] != "警告" {
		t.Fatal("read mutated shared summary")
	}
	c.put("b", value)
	c.get("a")
	c.put("c", value)
	if _, ok := c.get("b"); ok || len(c.entries) != 2 || c.bytes > c.maxBytes {
		t.Fatal("LRU capacity not enforced")
	}
	small := inventoryScreenCache{maxBytes: 1}
	small.put("oversize", value)
	if len(small.entries) != 0 || small.bytes != 0 {
		t.Fatal("oversized entry cached")
	}
}

func TestInventoryCacheIdentityAndCurrentExclusions(t *testing.T) {
	s := &Store{}
	run := func(user string, v MatchSnapshot) {
		t.Helper()
		screener := matching.NewLocalScreener(v.Profile, v.Candidate)
		mustScreen := s.screenInventory(context.Background(), user, &v, screener)
		if mustScreen != nil {
			t.Fatal(mustScreen)
		}
		for _, row := range v.Jobs {
			want := cloneInventoryScreen(screener.ScreenSummary(row.Job, row.Text))
			if !reflect.DeepEqual(*row.Local, want) || row.PreliminaryScore != want.Score {
				t.Fatal("cached screen differed from current fresh screen", row.Job.ID)
			}
		}
		if v.Jobs[1].ExcludedReason != "你已忽略这个岗位" {
			t.Fatal("cache overwrote current exclusion")
		}
	}
	run("alice", inventoryFixture())
	run("alice", inventoryFixture())
	if len(s.inventoryScreens.entries) != 2 {
		t.Fatal("identical input did not reuse summary")
	}
	variants := []MatchSnapshot{inventoryFixture(), inventoryFixture(), inventoryFixture(), inventoryFixture()}
	variants[0].Profile.Languages = []string{"JavaScript"}
	variants[1].Profile.TargetRoles = []string{"前端开发"}
	variants[2].Profile.Degree, variants[2].Profile.GraduationYear = "BACHELOR", 2026
	variants[3].Profile.PreferredCities = []string{"北京"}
	for _, v := range variants {
		run("alice", v)
	}
	v := inventoryFixture()
	v.CandidateHash = "reviewed-v2"
	run("alice", v)
	run("bob", inventoryFixture())
	v = inventoryFixture()
	v.Jobs[0].Text, v.Jobs[0].InputKey = "需要 JavaScript 和 React，开发前端页面。", "new-jd-input"
	run("alice", v)
	if len(s.inventoryScreens.entries) != 15 {
		t.Fatal("user, profile, reviewed candidate or JD change reused wrong identity", len(s.inventoryScreens.entries))
	}
}

func TestInventoryScreensConcurrentAndCancelWithoutLeakingSlots(t *testing.T) {
	s := &Store{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := inventoryFixture()
			if err := s.screenInventory(context.Background(), fmt.Sprint(i%2), &v, matching.NewLocalScreener(v.Profile, v.Candidate)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(s.inventoryScreens.entries) != 4 || len(s.inventoryScreens.slots) != 0 {
		t.Fatal("concurrent requests leaked CPU slots or cache keys")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := inventoryFixture()
	if err := s.screenInventory(ctx, "cancelled", &v, matching.NewLocalScreener(v.Profile, v.Candidate)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled read returned a successful partial list", err)
	}
	if len(s.inventoryScreens.slots) != 0 {
		t.Fatal("cancelled request leaked CPU slots")
	}
}
