package source

import (
	"context"
	"encoding/json"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const graduatePageSize = 10

var baiduPostID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type baiduPost struct {
	ID          string `json:"postId"`
	Name        string `json:"name"`
	Place       string `json:"workPlace"`
	Project     string `json:"projectType"`
	ProjectCode string `json:"projectTypeCode"`
	Duty        string `json:"workContent"`
	Requirement string `json:"serviceCondition"`
}
type baiduEnvelope[T any] struct {
	Status string `json:"status"`
	Data   T      `json:"data"`
}
type meituanPost struct {
	ID          string `json:"jobUnionId"`
	Name        string `json:"name"`
	JobType     string `json:"jobType"`
	Duty        string `json:"jobDuty"`
	Requirement string `json:"jobRequirement"`
	Cities      []struct {
		Name string `json:"name"`
	} `json:"cityList"`
}
type meituanEnvelope[T any] struct {
	Status int `json:"status"`
	Data   T   `json:"data"`
}

func baiduRef(v baiduPost) (PostingRef, error) {
	if !baiduPostID.MatchString(v.ID) || (v.ProjectCode != "1" && v.ProjectCode != "2" && v.ProjectCode != "3" && v.ProjectCode != "4") || v.Project == "" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: v.ID, URL: "https://talent.baidu.com/jobs/detail/GRADUATE/" + v.ID, Title: v.Name, Company: "百度", JobType: "FULL_TIME", Locations: splitXHSLocations(v.Place)}
	return r, validateRef(r)
}
func meituanRef(v meituanPost) (PostingRef, error) {
	if !xhsID.MatchString(v.ID) || v.JobType != "1" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	places := []string{}
	for _, c := range v.Cities {
		if strings.TrimSpace(c.Name) != "" {
			places = append(places, c.Name)
		}
	}
	r := PostingRef{ExternalID: v.ID, URL: "https://zhaopin.meituan.com/web/position/detail?jobUnionId=" + v.ID, Title: v.Name, Company: "美团", JobType: "FULL_TIME", Locations: places}
	return r, validateRef(r)
}

func (a PublicPlatform) graduatePage(ctx context.Context, s d.Source, page int) ([]PostingRef, int, error) {
	refs := []PostingRef{}
	total, pages, gotPage, gotSize := -1, 0, 0, 0
	switch s.Adapter {
	case "baidu":
		if s.Tenant != "GRADUATE" {
			return nil, 0, fail("UNSUPPORTED", false, 0)
		}
		var v baiduEnvelope[struct {
			Total json.Number `json:"total"`
			Pages int         `json:"pages"`
			Page  int         `json:"pageNum"`
			Size  int         `json:"pageSize"`
			List  []baiduPost `json:"list"`
		}]
		input := url.Values{"recruitType": {"GRADUATE"}, "curPage": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(graduatePageSize)}, "keyWord": {""}, "projectType": {""}}
		if err := a.post(ctx, s, "https://talent.baidu.com/httservice/getPostListNew", input, &v); err != nil {
			return nil, 0, err
		}
		if v.Status == "no-auth" || v.Status == "need-login" {
			return nil, 0, fail("BLOCKED", false, 200)
		}
		if v.Status != "ok" || v.Data.List == nil {
			return nil, 0, fail("SCHEMA_INVALID", false, 200)
		}
		var err error
		total, err = strconv.Atoi(v.Data.Total.String())
		if err != nil {
			return nil, 0, fail("SCHEMA_INVALID", false, 200)
		}
		pages, gotPage, gotSize = v.Data.Pages, v.Data.Page, v.Data.Size
		for _, row := range v.Data.List {
			r, err := baiduRef(row)
			if err != nil {
				return nil, 0, err
			}
			refs = append(refs, r)
		}
	case "meituan":
		if s.Tenant != "graduate" {
			return nil, 0, fail("UNSUPPORTED", false, 0)
		}
		var v meituanEnvelope[struct {
			List []meituanPost `json:"list"`
			Page struct {
				Number int `json:"pageNo"`
				Size   int `json:"pageSize"`
				Total  int `json:"totalCount"`
				Pages  int `json:"totalPage"`
			} `json:"page"`
		}]
		input := map[string]any{"page": map[string]int{"pageNo": page, "pageSize": graduatePageSize}, "jobShareType": "1", "keywords": "", "cityList": []any{}, "department": []any{}, "jfJgList": []any{}, "jobType": []any{map[string]any{"code": "1", "subCode": []string{}}}, "typeCode": []string{}}
		if err := a.post(ctx, s, "https://zhaopin.meituan.com/api/official/job/getJobList", input, &v); err != nil {
			return nil, 0, err
		}
		if v.Status != 1 || v.Data.List == nil {
			return nil, 0, fail("SCHEMA_INVALID", false, 200)
		}
		total, pages, gotPage, gotSize = v.Data.Page.Total, v.Data.Page.Pages, v.Data.Page.Number, v.Data.Page.Size
		for _, row := range v.Data.List {
			r, err := meituanRef(row)
			if err != nil {
				return nil, 0, err
			}
			refs = append(refs, r)
		}
	default:
		return nil, 0, fail("UNSUPPORTED", false, 0)
	}
	if total > MaxPostings {
		return nil, 0, fail("CAPACITY", false, 200)
	}
	if total < 0 || gotPage != page || gotSize != graduatePageSize || pages != (total+graduatePageSize-1)/graduatePageSize || len(refs) != min(graduatePageSize, max(0, total-(page-1)*graduatePageSize)) {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	return refs, total, nil
}

func (a PublicPlatform) previewGraduate(ctx context.Context, site CampusSite) (CampusPreview, error) {
	tenant := "GRADUATE"
	if site.Adapter == "meituan" {
		tenant = "graduate"
	}
	s := d.Source{ID: site.Adapter + "-preview", Adapter: site.Adapter, Tenant: tenant, RateLimit: 30}
	refs, total, err := a.graduatePage(ctx, s, 1)
	if err != nil {
		return CampusPreview{}, err
	}
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: tenant, Total: total, Samples: refs[:min(5, len(refs))]}, nil
}

func (a PublicPlatform) discoverGraduate(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	refs, total, err := a.graduatePage(ctx, s, 1)
	if err != nil {
		return nil, err
	}
	for page := 2; page <= (total+graduatePageSize-1)/graduatePageSize; page++ {
		rows, count, err := a.graduatePage(ctx, s, page)
		if err != nil {
			return nil, err
		}
		if count != total {
			return nil, fail("SCHEMA_INVALID", false, 200)
		}
		refs = append(refs, rows...)
	}
	return refs, nil
}

func (a PublicPlatform) fetchGraduate(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if s.Adapter == "baidu" {
		if s.Tenant != "GRADUATE" || !baiduPostID.MatchString(r.ExternalID) || r.URL != "https://talent.baidu.com/jobs/detail/GRADUATE/"+r.ExternalID {
			return "", fail("SCHEMA_INVALID", false, 0)
		}
		var v baiduEnvelope[baiduPost]
		if err := a.get(ctx, s, "https://talent.baidu.com/httservice/getPostDetail?"+url.Values{"recruitType": {"GRADUATE"}, "postId": {r.ExternalID}}.Encode(), &v); err != nil {
			return "", err
		}
		if v.Status == "no-auth" || v.Status == "need-login" {
			return "", fail("BLOCKED", false, 200)
		}
		ref, err := baiduRef(v.Data)
		if v.Status != "ok" || err != nil || ref.ExternalID != r.ExternalID || strings.TrimSpace(v.Data.Duty+v.Data.Requirement) == "" {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		return fmt.Sprintf("岗位名称：%s\n招聘项目：%s\n工作地点：%s\n工作职责：\n%s\n任职资格：\n%s", v.Data.Name, v.Data.Project, v.Data.Place, plainHTML(v.Data.Duty), plainHTML(v.Data.Requirement)), nil
	}
	if s.Adapter == "meituan" {
		if s.Tenant != "graduate" || !xhsID.MatchString(r.ExternalID) || r.URL != "https://zhaopin.meituan.com/web/position/detail?jobUnionId="+r.ExternalID {
			return "", fail("SCHEMA_INVALID", false, 0)
		}
		var v meituanEnvelope[meituanPost]
		if err := a.post(ctx, s, "https://zhaopin.meituan.com/api/official/job/getJobDetail", map[string]string{"jobUnionId": r.ExternalID, "jobShareType": "1"}, &v); err != nil {
			return "", err
		}
		ref, err := meituanRef(v.Data)
		if v.Status != 1 || err != nil || ref.ExternalID != r.ExternalID || strings.TrimSpace(v.Data.Duty+v.Data.Requirement) == "" {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		return fmt.Sprintf("岗位名称：%s\n招聘项目：美团应届生校招\n工作地点：%s\n工作职责：\n%s\n任职资格：\n%s", v.Data.Name, strings.Join(ref.Locations, "、"), plainHTML(v.Data.Duty), plainHTML(v.Data.Requirement)), nil
	}
	return "", fail("UNSUPPORTED", false, 0)
}
