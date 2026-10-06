package source

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const gbitsProject = "8a82ac07a057a3ea01a0617904b4100c"
const gbitsProjectName = "2027届秋季校园招聘"
const GBitsCampusURL = "https://hr.g-bits.com/web/index.html#/post-web/post-list/" + gbitsProject
const gbitsOrigin = "https://joinserverfast.g-bits.com"
const gbitsAPI = gbitsOrigin + "/humanResource/recruitmentExtranet"

type gbitsEnvelope[T any] struct {
	Status  *int `json:"status"`
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

func (v gbitsEnvelope[T]) valid() bool {
	return v.Status != nil && *v.Status == 10010 && v.Success
}

type gbitsJob struct {
	ID          string `json:"id"`
	Name        string `json:"postName"`
	Project     string `json:"recruitProjectId"`
	ProjectName string `json:"recruitProjectName"`
	Kind        string `json:"recruitmentType"`
	Status      string `json:"jobStatus"`
	Public      bool   `json:"isExternal"`
	City        string `json:"workAddress"`
	Original    string `json:"description"`
}

func gbitsJobURL(title string) string {
	// The public page expands originals inline; its published title-search route
	// is the human-facing link. Fetches bind the independent detail to its ID.
	return GBitsCampusURL + "?" + url.Values{"postName": {title}}.Encode()
}

func gbitsRef(j gbitsJob) (PostingRef, error) {
	if !hex32.MatchString(j.ID) || j.Project != gbitsProject || j.ProjectName != gbitsProjectName || j.Kind != "校招正式岗位招聘" || j.Status != "正常" || !j.Public {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	// Some formal campus positions require a prior internship. The public query
	// does not specify an employment type; keep it unknown and retain the original.
	r := PostingRef{ExternalID: j.ID, URL: gbitsJobURL(j.Name), Title: j.Name, Company: "吉比特&雷霆游戏", JobType: "UNKNOWN", Locations: splitPlaces(j.City)}
	return r, validateRef(r)
}

func (a PublicPlatform) discoverGBits(ctx context.Context, s d.Source) ([]PostingRef, error) {
	var project gbitsEnvelope[[]struct {
		ID   string `json:"projectId"`
		Name string `json:"projectName"`
	}]
	if err := a.post(ctx, s, gbitsAPI+"/ExtrannetHomePage/queryProjectList", map[string]any{}, &project); err != nil {
		return nil, err
	}
	matches := 0
	for _, p := range project.Data {
		if p.ID == gbitsProject && p.Name == gbitsProjectName {
			matches++
		}
	}
	if !project.valid() || matches != 1 {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v gbitsEnvelope[struct {
			Total *int       `json:"count"`
			Jobs  []gbitsJob `json:"list"`
		}]
		body := map[string]any{"currentPage": page, "pageSize": 50, "recruitsType": "CAMPUS_RECRUITING", "recruitProjectId": gbitsProject, "workPlace": nil, "postTypes": nil, "recruitmentType": nil, "sortField": "sortOrder", "isDescOrder": true}
		if err := a.post(ctx, s, gbitsAPI+"/ExtrannetCampusPost/queryRecuitPost", body, &v); err != nil {
			return campusPage{}, err
		}
		if !v.valid() || v.Data.Total == nil || v.Data.Jobs == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 50}
		for _, j := range v.Data.Jobs {
			r, err := gbitsRef(j)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}

func (a PublicPlatform) fetchGBits(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !hex32.MatchString(r.ExternalID) || r.URL != gbitsJobURL(r.Title) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v gbitsEnvelope[gbitsJob]
	if err := a.post(ctx, s, gbitsAPI+"/ExtrannetCampusPost/getPostDetail", map[string]any{"postId": r.ExternalID}, &v); err != nil {
		return "", err
	}
	actual, err := gbitsRef(v.Data)
	if err != nil || !v.valid() || actual.ExternalID != r.ExternalID || actual.Title != r.Title {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	original := plainHTML(v.Data.Original)
	if strings.TrimSpace(original) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	// Preserve the combined description, including responsibilities, requirements,
	// bonus points and early internship instructions. Ignore candidate questions.
	return fmt.Sprintf("岗位名称：%s\n公司：%s\n招聘范围：%s（校招正式岗位）\n工作地点：%s\n岗位原文：\n%s", actual.Title, actual.Company, gbitsProjectName, strings.Join(actual.Locations, "、"), original), nil
}
