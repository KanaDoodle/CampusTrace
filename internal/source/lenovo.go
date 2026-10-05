package source

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const lenovoOrigin = "https://talent.lenovo.com.cn"

type lenovoEnvelope[T any] struct {
	Code   *int `json:"code"`
	Result T    `json:"result"`
}
type lenovoPost struct {
	ID           int64  `json:"id"`
	Name         string `json:"jobName"`
	Project      *int   `json:"projectType"`
	Published    *int   `json:"publishFlag"`
	Active       *int   `json:"activateFlag"`
	Place        string `json:"workPlace"`
	Duties       string `json:"jobDuties"`
	Requirements string `json:"jobRequirement"`
}
type lenovoDict struct {
	Code     string `json:"dictCode"`
	Children []struct {
		Value int    `json:"dictValue"`
		Name  string `json:"dictName"`
	} `json:"children"`
}

func (a PublicPlatform) lenovoScope(ctx context.Context, s d.Source) (map[string]string, error) {
	var v lenovoEnvelope[[]struct {
		Type   int `json:"projectType"`
		Active int `json:"activateFlag"`
	}]
	if err := a.get(ctx, s, lenovoOrigin+"/gateway/proj/status", &v); err != nil {
		return nil, err
	}
	if v.Code == nil || *v.Code != 0 || v.Result == nil {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	count := 0
	for _, p := range v.Result {
		if p.Type == 1 && p.Active == 1 {
			count++
		}
	}
	if count != 1 {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	var dict lenovoEnvelope[[]lenovoDict]
	if err := a.get(ctx, s, lenovoOrigin+"/gateway/sysDict/all", &dict); err != nil {
		return nil, err
	}
	if dict.Code == nil || *dict.Code != 0 || dict.Result == nil {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	cities := map[string]string{}
	kind := ""
	cityGroups, projectGroups := 0, 0
	for _, group := range dict.Result {
		if group.Code == "city_portal" {
			cityGroups++
			for _, c := range group.Children {
				key := strconv.Itoa(c.Value)
				if c.Value <= 0 || c.Name == "" || cities[key] != "" {
					return nil, fail("SCHEMA_INVALID", false, 200)
				}
				cities[key] = c.Name
			}
		}
		if group.Code == "projectType" {
			projectGroups++
			for _, c := range group.Children {
				if c.Value == 1 {
					if kind != "" {
						return nil, fail("SCHEMA_INVALID", false, 200)
					}
					kind = c.Name
				}
			}
		}
	}
	if cityGroups != 1 || projectGroups != 1 || len(cities) == 0 || kind != "应届生招聘" {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return cities, nil
}
func lenovoRef(row lenovoPost, cities map[string]string) (PostingRef, error) {
	if row.ID <= 0 || row.Project == nil || *row.Project != 1 || row.Published == nil || *row.Published != 1 || row.Active == nil || *row.Active != 1 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	locations := []string{}
	seen := map[string]bool{}
	for _, code := range strings.Split(row.Place, ",") {
		code = strings.TrimSpace(code)
		if cities[code] == "" || seen[code] {
			return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
		}
		seen[code] = true
		locations = append(locations, cities[code])
	}
	id := strconv.FormatInt(row.ID, 10)
	ref := PostingRef{ExternalID: id, URL: lenovoOrigin + "/#/position/detail?id=" + id, Title: row.Name, Company: "联想", JobType: "FULL_TIME", Locations: locations}
	return ref, validateRef(ref)
}
func (a PublicPlatform) discoverLenovo(ctx context.Context, s d.Source) ([]PostingRef, error) {
	cities, err := a.lenovoScope(ctx, s)
	if err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v lenovoEnvelope[struct {
			Rows  []lenovoPost `json:"rows"`
			Total *int         `json:"total"`
		}]
		if err := a.get(ctx, s, fmt.Sprintf("%s/gateway/jobBase/list?pageNum=%d&pageSize=50&projectType=1", lenovoOrigin, page), &v); err != nil {
			return campusPage{}, err
		}
		if v.Code == nil || *v.Code != 0 || v.Result.Total == nil || v.Result.Rows == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Result.Total, Page: page, Size: 50}
		for _, row := range v.Result.Rows {
			ref, err := lenovoRef(row, cities)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchLenovo(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != lenovoOrigin+"/#/position/detail?id="+r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	cities, err := a.lenovoScope(ctx, s)
	if err != nil {
		return "", err
	}
	var v lenovoEnvelope[struct {
		Rows  []lenovoPost `json:"rows"`
		Total *int         `json:"total"`
	}]
	if err := a.get(ctx, s, lenovoOrigin+"/gateway/jobBase/list?jobId="+r.ExternalID, &v); err != nil {
		return "", err
	}
	if v.Code == nil || *v.Code != 0 || v.Result.Total == nil || *v.Result.Total != 1 || len(v.Result.Rows) != 1 {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	row := v.Result.Rows[0]
	ref, err := lenovoRef(row, cities)
	if err != nil || ref.ExternalID != r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return campusText(ref, "联想官网应届生招聘分类（不含人才专项、实习）", row.Duties, row.Requirements)
}
