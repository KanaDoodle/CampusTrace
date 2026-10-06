package source

import (
	"context"
	"fmt"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const TencentCampusURL = "https://join.qq.com/post.html"
const tencentOrigin = "https://join.qq.com"
const tencentScope = "2027_cn_1"

type tencentProject struct {
	MappingID int    `json:"mappingId"`
	Type      int    `json:"recruitType"`
	ProjectID string `json:"projectId"`
	Name      string `json:"projectName"`
	Year      string `json:"recruitYear"`
	Status    int    `json:"status"`
	Range     string `json:"recruitRangDesc"`
}

// Check the published mapping on every operation. Project ID 1 is reused by
// Tencent; a future year's project must not silently enter this fixed scope.
func (a PublicPlatform) tencentProject(ctx context.Context, s d.Source) (tencentProject, error) {
	var v struct {
		Status *int `json:"status"`
		Data   []struct {
			Type     int              `json:"recruitType"`
			Status   int              `json:"status"`
			Projects []tencentProject `json:"subProjectList"`
		} `json:"data"`
	}
	if err := a.get(ctx, s, tencentOrigin+"/api/v1/position/getProjectMapping", &v); err != nil {
		return tencentProject{}, err
	}
	if v.Status == nil || *v.Status != 0 || v.Data == nil {
		return tencentProject{}, fail("SCHEMA_INVALID", false, 200)
	}
	var project tencentProject
	found := 0
	for _, group := range v.Data {
		for _, item := range group.Projects {
			if item.MappingID == 1 && group.Type == 1 && group.Status == 1 && item.Type == 1 && item.Status == 1 && item.ProjectID == "1" && item.Year == "2027" && strings.TrimSpace(item.Name) != "" && strings.TrimSpace(item.Range) != "" {
				project, found = item, found+1
			}
		}
	}
	if found != 1 {
		return tencentProject{}, fail("SCHEMA_INVALID", false, 200)
	}
	return project, nil
}

type tencentListJob struct {
	ID        string `json:"postId"`
	Title     string `json:"positionTitle"`
	ProjectID *int   `json:"projectId"`
	Source    string `json:"positionSource"`
	Cities    string `json:"workCities"`
}

func tencentURL(id string) string { return tencentOrigin + "/post_detail.html?postid=" + id }

func tencentRef(job tencentListJob) (PostingRef, error) {
	if !numericID(job.ID) || job.ProjectID == nil || *job.ProjectID != 1 || job.Source != "oa" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	ref := PostingRef{ExternalID: job.ID, URL: tencentURL(job.ID), Title: strings.TrimSpace(job.Title), Company: "腾讯", JobType: "FULL_TIME", Locations: strings.Fields(job.Cities)}
	return ref, validateRef(ref)
}

func (a PublicPlatform) discoverTencent(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if _, err := a.tencentProject(ctx, s); err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v struct {
			Status *int `json:"status"`
			Data   struct {
				Count *int             `json:"count"`
				Jobs  []tencentListJob `json:"positionList"`
			} `json:"data"`
		}
		body := map[string]any{
			"projectIdList": []int{}, "projectMappingIdList": []int{1},
			"keyword": "", "bgList": []int{}, "workCountryType": 1,
			"workCityList": []int{}, "recruitCityList": []int{}, "positionFidList": []int{},
			"pageIndex": page, "pageSize": 100,
		}
		if err := a.post(ctx, s, tencentOrigin+"/api/v1/position/searchPosition", body, &v); err != nil {
			return campusPage{}, err
		}
		if v.Status == nil || *v.Status != 0 || v.Data.Count == nil || v.Data.Jobs == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Count, Page: page, Size: 100}
		for _, job := range v.Data.Jobs {
			ref, err := tencentRef(job)
			if err != nil {
				return campusPage{}, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}

func (a PublicPlatform) fetchTencent(ctx context.Context, s d.Source, ref PostingRef) (string, error) {
	if !numericID(ref.ExternalID) || ref.URL != tencentURL(ref.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	project, err := a.tencentProject(ctx, s)
	if err != nil {
		return "", err
	}
	var v struct {
		Status *int `json:"status"`
		Data   struct {
			ID        string   `json:"postId"`
			Title     string   `json:"title"`
			ProjectID *int     `json:"projectId"`
			Type      *int     `json:"recruitType"`
			Qingyun   *int     `json:"isQingyun"`
			Cities    []string `json:"workCityList"`
			Duties    string   `json:"desc"`
			Require   string   `json:"request"`
			Bonus     string   `json:"graduateBonus"`
		} `json:"data"`
	}
	if err := a.get(ctx, s, tencentOrigin+"/api/v1/jobDetails/getJobDetailsByPostId?postId="+ref.ExternalID, &v); err != nil {
		return "", err
	}
	job := v.Data
	if v.Status == nil || *v.Status != 0 || job.ID != ref.ExternalID || strings.TrimSpace(job.Title) != ref.Title || job.ProjectID == nil || *job.ProjectID != 1 || job.Type == nil || *job.Type != 1 || job.Qingyun == nil || *job.Qingyun != 0 {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	actual := ref
	actual.Company, actual.JobType, actual.Locations = "腾讯", "FULL_TIME", job.Cities
	text, err := campusText(actual, plainHTML(project.Name)+" · 官网中国工作地点分类；"+plainHTML(project.Range), job.Duties, job.Require)
	if err != nil {
		return "", err
	}
	if bonus := strings.TrimSpace(plainHTML(job.Bonus)); bonus != "" {
		text += fmt.Sprintf("\n加分项：\n%s", bonus)
	}
	return text, nil
}
