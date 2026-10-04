package source

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type jdEnvelope[T any] struct {
	Success bool `json:"success"`
	Body    T    `json:"body"`
}
type jdPost struct {
	ID          int64  `json:"publishId"`
	PlanID      int64  `json:"planId"`
	Name        string `json:"positionName"`
	RecruitType string `json:"recruitType"`
	Duty        string `json:"workContent"`
	Requirement string `json:"qualification"`
	Places      []struct {
		City string `json:"workCity"`
	} `json:"requirementVoList"`
}
type neteaseEnvelope[T any] struct {
	Code int `json:"code"`
	Data T   `json:"data"`
}
type neteasePost struct {
	ID          int64  `json:"id"`
	ProjectID   int    `json:"projectId"`
	Name        string `json:"positionName"`
	Place       string `json:"workPlaceName"`
	Duty        string `json:"positionDescription"`
	Requirement string `json:"positionRequirement"`
}

func jdRef(v jdPost) (PostingRef, error) {
	if v.ID <= 0 || v.PlanID <= 0 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	places := []string{}
	seen := map[string]bool{}
	for _, p := range v.Places {
		city := p.City
		// JD publishes province-city labels. Retain unknown formats, but use
		// the explicitly named city for the existing preference comparison.
		if province, name, ok := strings.Cut(city, "-"); ok && !strings.Contains(name, "-") && (strings.HasSuffix(province, "省") || strings.HasSuffix(province, "市") || strings.HasSuffix(province, "自治区")) && strings.HasSuffix(name, "市") {
			city = name
		}
		if city != "" && !seen[city] {
			places = append(places, city)
			seen[city] = true
		}
	}
	r := PostingRef{ExternalID: strconv.FormatInt(v.ID, 10), URL: "https://campus.jd.com/#/details?id=" + strconv.FormatInt(v.ID, 10), Title: v.Name, Company: "京东", JobType: "FULL_TIME", Locations: places}
	return r, validateRef(r)
}
func neteaseRef(v neteasePost) (PostingRef, error) {
	if v.ID <= 0 || v.ProjectID != 103 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: strconv.FormatInt(v.ID, 10), URL: "https://campus.163.com/app/detail/index?id=" + strconv.FormatInt(v.ID, 10) + "&projectId=103", Title: v.Name, Company: "网易互联网", JobType: "FULL_TIME", Locations: splitXHSLocations(v.Place)}
	return r, validateRef(r)
}

// Resolve the published graduate projects, so a changing plan number cannot
// silently bring internships into the JD graduate scope.
func (a PublicPlatform) jdPlans(ctx context.Context, s d.Source) (map[int64]bool, error) {
	var v jdEnvelope[struct {
		Projects []struct {
			Code    string `json:"code"`
			Release bool   `json:"release"`
			Groups  []struct {
				Plans []struct {
					ID int64 `json:"id"`
				} `json:"planMapList"`
			} `json:"groupList"`
		} `json:"projectList"`
	}]
	if err := a.get(ctx, s, "https://campus.jd.com/api/wx/position/getProjectList", &v); err != nil {
		return nil, err
	}
	if !v.Success || v.Body.Projects == nil {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	plans := map[int64]bool{}
	scopes := 0
	for _, project := range v.Body.Projects {
		if project.Code != "present" || !project.Release {
			continue
		}
		scopes++
		for _, group := range project.Groups {
			for _, plan := range group.Plans {
				if plan.ID <= 0 || plans[plan.ID] {
					return nil, fail("SCHEMA_INVALID", false, 200)
				}
				plans[plan.ID] = true
			}
		}
	}
	if scopes != 1 || len(plans) == 0 {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return plans, nil
}

func (a PublicPlatform) verifyNeteaseProject(ctx context.Context, s d.Source) error {
	var v neteaseEnvelope[[]struct {
		Title    string `json:"title"`
		Children []struct {
			Title string `json:"title"`
			Link  string `json:"link"`
		} `json:"children"`
	}]
	if err := a.get(ctx, s, "https://campus.163.com/api/campuspc/project/navigation/list", &v); err != nil {
		return err
	}
	if v.Code == 401 || v.Code == 403 {
		return fail("BLOCKED", false, 200)
	}
	if v.Code != 200 || v.Data == nil {
		return fail("SCHEMA_INVALID", false, 200)
	}
	count := 0
	for _, parent := range v.Data {
		if parent.Title == "应届生" {
			for _, child := range parent.Children {
				if child.Link == NeteaseCampusURL && child.Title == "网易互联网2027届校园招聘" {
					count++
				}
			}
		}
	}
	if count != 1 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}

func (a PublicPlatform) portalScope(ctx context.Context, s d.Source) (map[int64]bool, error) {
	if _, err := PlatformURL(s); err != nil {
		return nil, err
	}
	switch s.Adapter {
	case "jd":
		return a.jdPlans(ctx, s)
	case "netease":
		return nil, a.verifyNeteaseProject(ctx, s)
	default:
		return nil, fail("UNSUPPORTED", false, 0)
	}
}
func (a PublicPlatform) portalPage(ctx context.Context, s d.Source, page int, plans map[int64]bool) ([]PostingRef, int, error) {
	refs := []PostingRef{}
	total := -1
	switch s.Adapter {
	case "jd":
		var v jdEnvelope[struct {
			Total *int     `json:"totalNumber"`
			Items []jdPost `json:"items"`
		}]
		input := map[string]any{"pageSize": 10, "pageIndex": page - 1, "parameter": map[string]any{"positionName": "", "planIdList": []int{}, "jobDirectionCodeList": []string{}, "workCityCodeList": []string{}, "positionDeptList": []string{}}}
		if err := a.post(ctx, s, "https://campus.jd.com/api/wx/position/page?type=present", input, &v); err != nil {
			return nil, 0, err
		}
		if !v.Success || v.Body.Total == nil || v.Body.Items == nil {
			return nil, 0, fail("SCHEMA_INVALID", false, 200)
		}
		total = *v.Body.Total
		for _, row := range v.Body.Items {
			if !plans[row.PlanID] {
				return nil, 0, fail("SCHEMA_INVALID", false, 200)
			}
			ref, err := jdRef(row)
			if err != nil {
				return nil, 0, err
			}
			refs = append(refs, ref)
		}
	case "netease":
		rows, count, err := a.neteaseRows(ctx, s, page, "")
		if err != nil {
			return nil, 0, err
		}
		total = count
		for _, row := range rows {
			ref, err := neteaseRef(row)
			if err != nil {
				return nil, 0, err
			}
			refs = append(refs, ref)
		}
	default:
		return nil, 0, fail("UNSUPPORTED", false, 0)
	}
	if total > MaxPostings {
		return nil, 0, fail("CAPACITY", false, 200)
	}
	if total < 0 || len(refs) != min(10, max(0, total-(page-1)*10)) {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	return refs, total, nil
}
func (a PublicPlatform) neteaseRows(ctx context.Context, s d.Source, page int, id string) ([]neteasePost, int, error) {
	var v neteaseEnvelope[struct {
		Total *int          `json:"total"`
		Pages int           `json:"pages"`
		List  []neteasePost `json:"list"`
	}]
	q := url.Values{"projectId": {"103"}, "currentPage": {strconv.Itoa(page)}, "pageSize": {"10"}}
	if id != "" {
		q.Set("positionIdList", id)
	}
	if err := a.get(ctx, s, "https://campus.163.com/api/campuspc/position/getJobList?"+q.Encode(), &v); err != nil {
		return nil, 0, err
	}
	if v.Code == 401 || v.Code == 403 {
		return nil, 0, fail("BLOCKED", false, 200)
	}
	if v.Code != 200 || v.Data.Total == nil || v.Data.List == nil || *v.Data.Total < 0 || v.Data.Pages != (*v.Data.Total+9)/10 {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	return v.Data.List, *v.Data.Total, nil
}
func (a PublicPlatform) previewPortal(ctx context.Context, site CampusSite) (CampusPreview, error) {
	tenant := "present"
	if site.Adapter == "netease" {
		tenant = "103"
	}
	s := d.Source{ID: site.Adapter + "-preview", Adapter: site.Adapter, Tenant: tenant, RateLimit: 30}
	plans, err := a.portalScope(ctx, s)
	if err != nil {
		return CampusPreview{}, err
	}
	refs, total, err := a.portalPage(ctx, s, 1, plans)
	if err != nil {
		return CampusPreview{}, err
	}
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: tenant, Total: total, Samples: refs[:min(5, len(refs))]}, nil
}
func (a PublicPlatform) discoverPortal(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	plans, err := a.portalScope(ctx, s)
	if err != nil {
		return nil, err
	}
	refs, total, err := a.portalPage(ctx, s, 1, plans)
	if err != nil {
		return nil, err
	}
	for page := 2; page <= (total+9)/10; page++ {
		rows, count, err := a.portalPage(ctx, s, page, plans)
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
func (a PublicPlatform) fetchPortal(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !xhsID.MatchString(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	switch s.Adapter {
	case "jd":
		if s.Tenant != "present" || r.URL != "https://campus.jd.com/#/details?id="+r.ExternalID {
			return "", fail("SCHEMA_INVALID", false, 0)
		}
		var v jdEnvelope[jdPost]
		if err := a.post(ctx, s, "https://campus.jd.com/api/wx/position/detail/"+r.ExternalID, map[string]any{}, &v); err != nil {
			return "", err
		}
		ref, err := jdRef(v.Body)
		if !v.Success || err != nil || ref.ExternalID != r.ExternalID || v.Body.RecruitType != "应届生" || strings.TrimSpace(v.Body.Duty) == "" || strings.TrimSpace(v.Body.Requirement) == "" {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		clean := func(s string) string {
			return plainHTML(strings.ReplaceAll(strings.ReplaceAll(s, `\r\n`, "\n"), `\n`, "\n"))
		}
		originalPlaces := []string{}
		seen := map[string]bool{}
		for _, place := range v.Body.Places {
			if place.City != "" && !seen[place.City] {
				originalPlaces = append(originalPlaces, place.City)
				seen[place.City] = true
			}
		}
		return fmt.Sprintf("岗位名称：%s\n招聘项目：京东应届生项目\n工作地点：%s\n工作职责：\n%s\n任职资格：\n%s", v.Body.Name, strings.Join(originalPlaces, "、"), clean(v.Body.Duty), clean(v.Body.Requirement)), nil
	case "netease":
		if s.Tenant != "103" || r.URL != "https://campus.163.com/app/detail/index?id="+r.ExternalID+"&projectId=103" {
			return "", fail("SCHEMA_INVALID", false, 0)
		}
		// The public list already contains full descriptions and requirements.
		// Query its published positionIdList filter; never call the login-only
		// detail endpoint or persist credentials to retrieve extra fields.
		rows, total, err := a.neteaseRows(ctx, s, 1, r.ExternalID)
		if err != nil {
			return "", err
		}
		if total != 1 || len(rows) != 1 {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		v := rows[0]
		ref, err := neteaseRef(v)
		if err != nil || ref.ExternalID != r.ExternalID || strings.TrimSpace(v.Duty) == "" || strings.TrimSpace(v.Requirement) == "" {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		return fmt.Sprintf("岗位名称：%s\n招聘项目：网易互联网2027届校园招聘\n工作地点：%s\n工作职责：\n%s\n任职资格：\n%s", v.Name, v.Place, plainHTML(v.Duty), plainHTML(v.Requirement)), nil
	}
	return "", fail("UNSUPPORTED", false, 0)
}
