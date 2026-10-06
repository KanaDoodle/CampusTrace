package source

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const cecOrigin = "https://campus.cec.com.cn"
const CECCampusURL = cecOrigin + "/position?positionType=0"
const cecScope = "graduate_software"

// The group-wide campus listing exceeds capacity. This preset explicitly
// covers these two verified software units, not the entire CEC group.
var cecUnits = map[int64]string{579425410220035: "麒麟软件有限公司", 579425410220074: "中电云计算技术有限公司"}

type cecEnvelope[T any] struct {
	Code string `json:"code"`
	Data T      `json:"data"`
}
type cecJob struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Kind   *int   `json:"positionType"`
	City   string `json:"cityName"`
	Org    string `json:"org"`
	Nature string `json:"workNature"`
}

func cecJobURL(id string) string { return cecOrigin + "/positionDetail?id=" + id }
func cecRef(v cecJob) (PostingRef, error) {
	known := false
	for _, name := range cecUnits {
		known = known || v.Org == name
	}
	if !numericID(v.ID) || !known || v.Kind == nil || *v.Kind != 0 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	kind := "UNKNOWN"
	if v.Nature == "全职" {
		kind = "FULL_TIME"
	}
	if v.Nature == "实习" {
		kind = "INTERNSHIP"
	}
	r := PostingRef{ExternalID: v.ID, URL: cecJobURL(v.ID), Title: v.Name, Company: v.Org, JobType: kind, Locations: splitPlaces(v.City)}
	return r, validateRef(r)
}
func (a PublicPlatform) verifyCEC(ctx context.Context, s d.Source) error {
	type org struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Children []org  `json:"children"`
	}
	var v cecEnvelope[[]org]
	if err := a.get(ctx, s, cecOrigin+"/student-api/api/common/listOrg", &v); err != nil {
		return err
	}
	if v.Code != "000000" || len(v.Data) != 1 || v.Data[0].ID != 464790010986497 || v.Data[0].Name != "中国电子信息产业集团有限公司" {
		return fail("SCHEMA_INVALID", false, 200)
	}
	seen := map[int64]int{}
	var walk func([]org)
	walk = func(rows []org) {
		for _, row := range rows {
			if name, ok := cecUnits[row.ID]; ok && row.Name == name {
				seen[row.ID]++
			}
			walk(row.Children)
		}
	}
	walk(v.Data)
	for id := range cecUnits {
		if seen[id] != 1 {
			return fail("SCHEMA_INVALID", false, 200)
		}
	}
	return nil
}
func (a PublicPlatform) discoverCEC(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.verifyCEC(ctx, s); err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v cecEnvelope[struct {
			Total *int     `json:"total"`
			Page  int      `json:"current"`
			Size  int      `json:"size"`
			Pages int      `json:"pages"`
			Rows  []cecJob `json:"records"`
		}]
		body := map[string]any{"page": page, "size": 50, "positionType": "0", "orgId": []int64{579425410220035, 579425410220074}}
		if err := a.post(ctx, s, cecOrigin+"/student-api/api/position/search", body, &v); err != nil {
			return campusPage{}, err
		}
		if v.Code != "000000" || v.Data.Total == nil || v.Data.Rows == nil || v.Data.Page != page || v.Data.Size != 50 || v.Data.Pages != (*v.Data.Total+49)/50 {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 50}
		for _, row := range v.Data.Rows {
			r, err := cecRef(row)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchCEC(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != cecJobURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	if err := a.verifyCEC(ctx, s); err != nil {
		return "", err
	}
	var v cecEnvelope[struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		OrgID    int64  `json:"orgId"`
		Org      string `json:"orgName"`
		Kind     *int   `json:"positionType"`
		Nature   string `json:"workNature"`
		City     string `json:"cityName"`
		Duty     string `json:"jobDescription"`
		Require  string `json:"jobRequirements"`
		Benefits string `json:"salaryRequirements"`
		Number   string `json:"positionNo"`
		Deadline string `json:"closeDate"`
	}]
	if err := a.get(ctx, s, cecOrigin+"/student-api/api/position/find/"+r.ExternalID, &v); err != nil {
		return "", err
	}
	row := v.Data
	actual, err := cecRef(cecJob{ID: strconv.FormatInt(row.ID, 10), Name: row.Name, Kind: row.Kind, City: row.City, Org: row.Org, Nature: row.Nature})
	if v.Code != "000000" || err != nil || cecUnits[row.OrgID] != row.Org || actual.ExternalID != r.ExternalID || actual.Title != r.Title || actual.Company != r.Company || strings.TrimSpace(plainHTML(row.Duty)) == "" || strings.TrimSpace(plainHTML(row.Require)) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return fmt.Sprintf("岗位名称：%s\n招聘单位：%s\n招聘分类：校园招聘（具体毕业届别以岗位原文为准）\n用工类型：%s\n工作地点：%s\n岗位编号：%s\n官网公开投递截止日期：%s\n岗位职责：\n%s\n任职要求：\n%s\n薪酬福利：\n%s", actual.Title, actual.Company, row.Nature, strings.Join(actual.Locations, "、"), row.Number, row.Deadline, plainHTML(row.Duty), plainHTML(row.Require), plainHTML(row.Benefits)), nil
}
