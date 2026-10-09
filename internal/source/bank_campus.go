package source

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"golang.org/x/net/html"
)

const CMBCampusURL = "https://career.cmbchina.com/positionlist/96574F8D-C7ED-4772-AE7C-BAC896D190C1"
const CITICCampusURL = "https://job.citicbank.com/CustStyle/zpmhys/schoolRecruit.html"
const cmbOrigin = "https://career.cmbchina.com"
const cmbGraduate = "96574F8D-C7ED-4772-AE7C-BAC896D190C1"
const citicOrigin = "https://job.citicbank.com"

// Scan the complete graduate category before selecting the advertised subset.
// A generic "技术" also covers aircraft/ship leasing, so it is not an IT signal.
var bankTechnologyTitle = regexp.MustCompile(`信息技术|金融科技|软件|系统研发|系统开发|智能.*研发|运维|全栈|算法|数据分析|数据研发|网络安全`)
var bankUUID = regexp.MustCompile(`^[A-F0-9]{8}-[A-F0-9]{4}-[A-F0-9]{4}-[A-F0-9]{4}-[A-F0-9]{12}$`)

type cmbEnvelope[T any] struct {
	Code string `json:"returnCode"`
	Body T      `json:"body"`
}
type cmbJob struct {
	ID           string `json:"publishGID"`
	Kind         string `json:"recruitmentTypeID"`
	Title        string `json:"jobDisplay"`
	UnitCode     string `json:"branchCode"`
	Unit         string `json:"branchCodeName"`
	City         string `json:"locationName"`
	Deadline     string `json:"expiredOn"`
	Duties       string `json:"jobResponsibility"`
	Requirements string `json:"jobRequirement"`
}

func cmbJobURL(id string) string { return cmbOrigin + "/positionDetail/school?publishId=" + id }
func cmbRef(v cmbJob) (PostingRef, error) {
	if !bankUUID.MatchString(v.ID) || !numericID(v.UnitCode) || strings.TrimSpace(v.Unit) == "" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: v.ID, URL: cmbJobURL(v.ID), Title: v.Title, Company: "招商银行 · " + v.Unit, JobType: "UNKNOWN", Locations: splitPlaces(v.City)}
	return r, validateRef(r)
}
func (a PublicPlatform) discoverCMB(ctx context.Context, s d.Source) ([]PostingRef, error) {
	all, err := campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v cmbEnvelope[struct {
			Total *int     `json:"total"`
			Rows  []cmbJob `json:"data"`
		}]
		body := map[string]any{"pageIndex": page, "pageSize": 50, "recruitmentTypeId": cmbGraduate, "keywords": "", "orgIdList": []string{}, "locationIdList": []string{}}
		if err := a.post(ctx, s, cmbOrigin+"/api/campusRecruitmentWebsite/job/getList", body, &v); err != nil {
			return campusPage{}, err
		}
		if v.Code != "SUC0000" || v.Body.Total == nil || v.Body.Rows == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Body.Total, Page: page, Size: 50}
		for _, row := range v.Body.Rows {
			ref, err := cmbRef(row)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	refs := []PostingRef{}
	for _, ref := range all {
		if bankTechnologyTitle.MatchString(ref.Title) {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}
func (a PublicPlatform) fetchCMB(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !bankUUID.MatchString(r.ExternalID) || r.URL != cmbJobURL(r.ExternalID) || !bankTechnologyTitle.MatchString(r.Title) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v cmbEnvelope[cmbJob]
	if err := a.get(ctx, s, cmbOrigin+"/api/campusRecruitmentWebsite/job/getDetail?publishId="+r.ExternalID, &v); err != nil {
		return "", err
	}
	actual, err := cmbRef(v.Body)
	if err != nil || v.Code != "SUC0000" || v.Body.Kind != cmbGraduate || actual.ExternalID != r.ExternalID || actual.Title != r.Title || actual.Company != r.Company {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text, err := bankPostingText(actual, "官网应届生分类 · 按岗位名称筛选科技与研发方向（含培养生，具体届别、轮岗安排以原文为准）", v.Body.Duties, v.Body.Requirements)
	if err == nil && v.Body.Deadline != "" {
		text += "\n官网截止日期：" + v.Body.Deadline
	}
	return text, err
}

type citicRow struct {
	ID    int    `json:"ID"`
	Title string `json:"RELEASENAME"`
	Unit  string `json:"CONTENT"`
	City  string `json:"WORKADDR"`
}

func citicJobURL(id string) string { return citicOrigin + "/static/positionDetail_" + id + "_02.html" }
func citicRef(v citicRow) (PostingRef, error) {
	if v.ID <= 0 || !strings.Contains(v.Title, "信息科技") || strings.TrimSpace(v.Unit) == "" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: strconv.Itoa(v.ID), URL: citicJobURL(strconv.Itoa(v.ID)), Title: v.Title, Company: "中信银行 · " + v.Unit, JobType: "UNKNOWN", Locations: splitPlaces(v.City)}
	return r, validateRef(r)
}
func (a PublicPlatform) discoverCITIC(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v struct {
			Success *bool `json:"IsSuc"`
			Total   *int  `json:"pageCount"` // Despite its name this is the row count; official pages contain 15 rows.
			Table   struct {
				Rows []struct {
					Item citicRow `json:"itemMap"`
				} `json:"rows"`
			} `json:"tableData"`
		}
		body := map[string]any{"RELEASENAME": "信息科技", "recruitmentType": "02", "workAddr": []string{}, "deptCode": []string{}, "page": page, "userId": nil}
		if err := a.post(ctx, s, citicOrigin+"/recruitportal/portal/recruitQuery", body, &v); err != nil {
			return campusPage{}, err
		}
		if v.Success == nil || !*v.Success || v.Total == nil || v.Table.Rows == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Total, Page: page, Size: 15}
		for _, row := range v.Table.Rows {
			ref, err := citicRef(row.Item)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchCITIC(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != citicJobURL(r.ExternalID) || !strings.Contains(r.Title, "信息科技") {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	root, err := a.document(ctx, s, r.URL, nil)
	if err != nil {
		return "", err
	}
	fields := map[string]string{}
	for _, id := range []string{"postName2", "WORKADDR", "ZY", "XL", "GZLX", "JOBDUTY", "RZYQ"} {
		node, err := uniqueNode(root, func(n *html.Node) bool { return nodeAttr(n, "id") == id })
		if err != nil || nodeText(node) == "" {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		fields[id] = nodeText(node)
	}
	if fields["postName2"] != r.Title || fields["GZLX"] != "全职" || !strings.Contains(fields["RZYQ"], "应届") {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	r.Locations, r.JobType = splitPlaces(fields["WORKADDR"]), "FULL_TIME"
	text, err := bankPostingText(r, "官网校园招聘 · 信息科技岗位（具体届别及培养安排见原文）", fields["JOBDUTY"], fields["RZYQ"])
	return text + "\n官网学历要求：" + fields["XL"] + "\n官网专业要求：" + fields["ZY"], err
}
func bankPostingText(r PostingRef, scope, duties, requirements string) (string, error) {
	duties, requirements = plainHTML(duties), plainHTML(requirements)
	if strings.TrimSpace(duties) == "" || strings.TrimSpace(requirements) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return fmt.Sprintf("岗位名称：%s\n招聘单位：%s\n招聘范围：%s\n工作地点：%s\n岗位职责：\n%s\n任职要求：\n%s", r.Title, r.Company, scope, strings.Join(r.Locations, "、"), duties, requirements), nil
}

func bankEntry(site CampusSite, u *url.URL) bool {
	if u.Scheme != "https" || u.RawFragment != "" {
		return false
	}
	if site.Adapter == "cmb_tech" && u.Scheme == "https" && u.Host == "career.cmbchina.com" && u.RawQuery == "" && u.Fragment == "" {
		return strings.TrimRight(u.Path, "/") == "" || u.String() == CMBCampusURL
	}
	if site.Adapter == "boc_software" && u.Host == "campus.chinahr.com" && strings.TrimRight(u.Path, "/") == "/pages/2027-boc" && u.RawQuery == "" && (u.Fragment == "" || u.Fragment == "/jobs") {
		return true
	}
	return u.String() == site.URL
}
