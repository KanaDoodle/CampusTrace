package source

import (
	"context"
	"net/url"
	"strconv"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const hotjobOrigin = "https://wecruit.hotjob.cn"

var securitiesProjects = map[string]struct{ Suite, Project, Name, Company string }{
	"cms_securities":  {"SU629dbc0c0dcad452299bc0f7", "101501", "2027校园招聘", "招商证券"},
	"htsc_securities": {"SU6419745cbef57c635fe10142", "107301", "2026年秋招（总部）", "华泰证券 · 总部"},
}

func isSecuritiesBeisen(adapter string) bool {
	switch adapter {
	case "csc_securities", "guosen_securities", "galaxy_securities", "cicc_securities":
		return true
	}
	return false
}
func securitiesCampusURL(adapter string) string {
	p := securitiesProjects[adapter]
	return hotjobOrigin + "/" + p.Suite + "/pb/school.html?projectCode=" + p.Project
}
func securitiesJobURL(adapter, id string) string {
	return hotjobOrigin + "/" + securitiesProjects[adapter].Suite + "/pb/posDetail.html?postId=" + id + "&postType=campus"
}
func securitiesEndpoint(adapter, name string) string {
	return hotjobOrigin + "/wecruit/positionInfo/" + name + "/" + securitiesProjects[adapter].Suite + "?iSaJAx=isAjax&request_locale=zh_CN"
}
func securitiesRef(adapter string, row honorPost) (PostingRef, error) {
	project, ok := securitiesProjects[adapter]
	if !ok || !bankHex24.MatchString(row.ID) || row.Recruit == nil || *row.Recruit != 1 || strconv.Itoa(row.Project) != project.Project || row.ProjectName != project.Name || row.WorkType != "全职" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	ref := PostingRef{ExternalID: row.ID, URL: securitiesJobURL(adapter, row.ID), Title: row.Name, Company: project.Company, JobType: "FULL_TIME", Locations: splitPlaces(row.Place)}
	if row.Place == "全部地区" {
		ref.Locations = []string{}
	}
	return ref, validateRef(ref)
}
func (a PublicPlatform) discoverSecurities(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v honorEnvelope[struct {
			Form struct {
				Rows  []honorPost `json:"pageData"`
				Total *int        `json:"dataCount"`
				Page  int         `json:"currentPage"`
				Size  int         `json:"pageSize"`
				Pages int         `json:"totalPage"`
			} `json:"pageForm"`
		}]
		input := url.Values{"isFrompb": {"true"}, "recruitType": {"1"}, "projectCode": {securitiesProjects[s.Adapter].Project}, "pageSize": {"15"}, "currentPage": {strconv.Itoa(page)}}
		if err := a.post(ctx, s, securitiesEndpoint(s.Adapter, "listPosition"), input, &v); err != nil {
			return campusPage{}, err
		}
		if err := honorOK(v); err != nil {
			return campusPage{}, err
		}
		f := v.Data.Form
		if f.Total == nil || f.Rows == nil || f.Size != 15 || f.Pages != (*f.Total+14)/15 {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *f.Total, Page: f.Page, Size: f.Size}
		for _, row := range f.Rows {
			ref, err := securitiesRef(s.Adapter, row)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchSecurities(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !bankHex24.MatchString(r.ExternalID) || r.URL != securitiesJobURL(s.Adapter, r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v honorEnvelope[honorPost]
	if err := a.post(ctx, s, securitiesEndpoint(s.Adapter, "listPositionDetail"), url.Values{"postId": {r.ExternalID}, "recruitType": {"1"}}, &v); err != nil {
		return "", err
	}
	if err := honorOK(v); err != nil {
		return "", err
	}
	actual, err := securitiesRef(s.Adapter, v.Data)
	if err != nil || actual.ExternalID != r.ExternalID || actual.Title != r.Title || actual.Company != r.Company {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text, err := bankPostingText(actual, "官网校园招聘 · "+v.Data.ProjectName+"（具体毕业范围、实习考察以原文为准）", v.Data.Duties, v.Data.Requirements)
	if err == nil {
		text += "\n官网用工类型：" + v.Data.WorkType
		if v.Data.Deadline != "" {
			text += "\n官网公布截止时间：" + v.Data.Deadline
		}
	}
	return text, err
}
