package matching

import (
	"context"
	"github.com/KanaDoodle/CampusTrace/internal/analysis"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"regexp"
	"strings"
)

func analysisClaims(text string) ([]d.Claim, error) {
	claims, err := analysis.Extract(context.Background(), text)
	if err != nil {
		return nil, err
	}
	kept := []d.Claim{}
	for _, c := range claims {
		if c.Type == "EDUCATION_REQUIREMENT" || c.Type == "GRADUATION_REQUIREMENT" {
			if strings.Contains(c.Excerpt, "优先") || strings.Contains(c.Excerpt, "加分") || strings.Contains(c.Excerpt, "不限") {
				continue
			}
			if c.Type == "GRADUATION_REQUIREMENT" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Excerpt)), "graduation:") {
				years := regexp.MustCompile(`20\d{2}`).FindAllString(c.Excerpt, -1)
				unique := map[string]bool{}
				for _, year := range years {
					unique[year] = true
				}
				if len(unique) > 1 {
					continue
				}
			}
		}
		kept = append(kept, c)
	}
	return kept, nil
}
