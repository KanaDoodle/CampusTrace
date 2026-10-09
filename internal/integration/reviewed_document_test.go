package integration

import (
	"errors"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"strings"
	"testing"
)

// Read source labels from the actual readable provider input in scripted tests.
func fixtureDocumentFacts(c matching.Candidate) []matching.Fact {
	if len(c.Facts) > 0 {
		return c.Facts
	}
	out := []matching.Fact{}
	for _, part := range strings.Split(c.Document, "\n【依据 ")[1:] {
		head, body, ok := strings.Cut(part, "】\n")
		if !ok {
			continue
		}
		id, kind, ok := strings.Cut(head, "｜")
		if !ok {
			continue
		}
		body, _, _ = strings.Cut(body, "\n##")
		body, _, _ = strings.Cut(body, "\n项目：")
		out = append(out, matching.Fact{ID: id, Kind: kind, Text: strings.TrimSuffix(body, "\n")})
	}
	return out
}

func TestReviewedDocumentIncludesCorrectionsAndInvalidatesOldInput(t *testing.T) {
	ctx, s, u, token, ids, _, h, _ := wholeSetup(t)
	old, err := s.MatchSnapshot(ctx, u, "fixture", "", ids)
	must(t, err)
	profile, err := s.Profile(ctx, u)
	must(t, err)
	extra := "实习经历：使用 Go 开发订单服务，实际实现幂等处理；没有生产压测结论。"
	profile.AdditionalExperience = &extra
	must(t, s.SaveProfile(ctx, u, profile))
	next, err := s.MatchSnapshot(ctx, u, "fixture", "", ids)
	must(t, err)
	if next.CandidateHash == old.CandidateHash || !strings.Contains(next.CandidateDocument, extra) {
		t.Fatal("reviewed correction missing")
	}
	rec := matchingRequest(h, token, "/api/profile/document", "GET", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), extra) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	profile, err = s.Profile(ctx, u)
	must(t, err)
	profile.AdditionalExperience = nil
	must(t, s.SaveProfile(ctx, u, profile))
	profile, err = s.Profile(ctx, u)
	must(t, err)
	if profile.AdditionalExperience == nil || *profile.AdditionalExperience != extra {
		t.Fatal("old client erased new field")
	}
	sensitive := "联系 person@example.com"
	profile.AdditionalExperience = &sensitive
	if err := s.SaveProfile(ctx, u, profile); !errors.Is(err, p.ErrValidation) {
		t.Fatal("sensitive supplement accepted", err)
	}
	profile.AdditionalExperience = new(string)
	must(t, s.SaveProfile(ctx, u, profile))
	final, err := s.MatchSnapshot(ctx, u, "fixture", "", ids)
	must(t, err)
	if strings.Contains(final.CandidateDocument, extra) {
		t.Fatal("deleted supplement survived")
	}
}
