package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
)

const mideaOrigin = "https://careers.midea.com"

type mideaEnvelope[T any] struct {
	Code string `json:"code"`
	Data T      `json:"data"`
}
type mideaProjectInfo struct {
	ID       string `json:"projectRuleId"`
	Name     string `json:"projectRuleName"`
	Type     string `json:"projectType"`
	Year     string `json:"numberOfSessions"`
	Category int    `json:"employementCategory"`
	Status   int    `json:"status"`
}
type mideaPost struct {
	ID        string `json:"positionId"`
	Project   string `json:"projectRuleId"`
	Type      string `json:"projectType"`
	Category  int    `json:"employementCategory"`
	Name      string `json:"projectPositionName"`
	Published *int   `json:"publishStatus"`
	Places    []struct {
		Name string `json:"workPlaceName"`
	} `json:"workplaceDtoList"`
	Position struct {
		Name         string `json:"positionName"`
		Duties       string `json:"jobResponsibility"`
		Requirements string `json:"jobRequirement"`
	} `json:"projectPositionDto"`
}

func (a PublicPlatform) mideaScope(ctx context.Context, s d.Source) error {
	var v mideaEnvelope[[]mideaProjectInfo]
	if err := a.get(ctx, s, mideaOrigin+"/backend/school/position/common/project/list", &v); err != nil {
		return err
	}
	if v.Code != "0" || v.Data == nil {
		return fail("SCHEMA_INVALID", false, 200)
	}
	count := 0
	for _, p := range v.Data {
		if p.ID == mideaProject {
			if p.Name != "2027届美的星校园招聘" || p.Type != "1" || p.Year != "2027" || p.Category != 1 || p.Status != 1 {
				return fail("SCHEMA_INVALID", false, 200)
			}
			count++
		}
	}
	if count != 1 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func mideaRef(row mideaPost, detail bool) (PostingRef, error) {
	if !hex32.MatchString(row.ID) || row.Project != mideaProject || row.Type != "1" || row.Category != 1 || row.Name != row.Position.Name || (detail && (row.Published == nil || *row.Published != 1)) {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	locations := []string{}
	for _, p := range row.Places {
		locations = append(locations, p.Name)
	}
	locations = splitPlaces(strings.Join(locations, "、"))
	ref := PostingRef{ExternalID: row.ID, URL: mideaOrigin + "/schoolOut/post/details?positionId=" + row.ID, Title: row.Name, Company: "美的集团", JobType: "FULL_TIME", Locations: locations}
	return ref, validateRef(ref)
}
func (a PublicPlatform) discoverMidea(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.mideaScope(ctx, s); err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v mideaEnvelope[struct {
			Rows  []mideaPost `json:"data"`
			Total *int        `json:"total"`
			Info  struct {
				Page  int `json:"pageIndex"`
				Size  int `json:"pageSize"`
				Pages int `json:"totalPage"`
			} `json:"info"`
		}]
		input := map[string]any{"keyword": "", "superiorIds": []any{}, "recruitCategoryIds": []any{}, "workPlaceCodes": []any{}, "projectRuleId": mideaProject, "pageIndex": page, "pageSize": 20}
		if err := a.post(ctx, s, mideaOrigin+"/backend/school/position/common/position/list", input, &v); err != nil {
			return campusPage{}, err
		}
		if v.Code != "0" || v.Data.Total == nil || v.Data.Rows == nil || v.Data.Info.Size != 20 || v.Data.Info.Pages != (*v.Data.Total+19)/20 {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: v.Data.Info.Page, Size: 20}
		for _, row := range v.Data.Rows {
			ref, err := mideaRef(row, false)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchMidea(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !hex32.MatchString(r.ExternalID) || r.URL != mideaOrigin+"/schoolOut/post/details?positionId="+r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	if err := a.mideaScope(ctx, s); err != nil {
		return "", err
	}
	var v mideaEnvelope[mideaPost]
	if err := a.post(ctx, s, mideaOrigin+"/backend/school/position/common/position/details", map[string]any{"positionId": r.ExternalID}, &v); err != nil {
		return "", err
	}
	if v.Code != "0" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	ref, err := mideaRef(v.Data, true)
	if err != nil || ref.ExternalID != r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return campusText(ref, "2027届美的星校园招聘（不含博士专项、实习）", v.Data.Position.Duties, v.Data.Position.Requirements)
}
