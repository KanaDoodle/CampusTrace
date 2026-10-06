package source

import (
	"context"
	"encoding/base64"
	"encoding/json"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

const ctOrigin = "https://job.chinatelecom.com.cn"
const ctProject = "101101"
const ctProjectName = "2027年度秋季校园招聘"

var ctUnits = map[string]struct{ ID, Name string }{"ctyun": {"581854", "天翼云科技有限公司"}, "ctcloud": {"581851", "中国电信云计算研究院"}}

func ctPublicURL(data any) string {
	b, _ := json.Marshal(data)
	encoded := strings.ReplaceAll(base64.StdEncoding.EncodeToString(b), "/", "~2F")
	return ctOrigin + "/wt/TELE/web/index?brandCode=1#/postinquiry?" + url.Values{"data": {encoded}}.Encode()
}
func ctCampusURL(adapter string) string {
	return ctPublicURL(map[string]any{"key": ctUnits[adapter].ID, "type": "1", "recruitProject": ctProject, "recruitProjectName": ctProjectName})
}
func ctJobURL(adapter, id, title string) string {
	// The portal publishes title-search routes instead of permanent single-job
	// links. The ID makes the original entry stable for duplicate titles. The
	// adapter obtains the actual original through the independent detail query.
	return ctPublicURL(map[string]any{"type": "1", "postName": title, "postId": id, "filters": map[string]string{"org": "[" + ctUnits[adapter].ID + "]", "recruitProject": "[" + ctProject + "]"}})
}

type ctEnvelope[T any] struct {
	Code string `json:"code"`
	Data T      `json:"data"`
}
type ctJob struct {
	ID   int64  `json:"PostId"`
	Name string `json:"PostName"`
	City string `json:"WorkPlace"`
	Org  string `json:"OrgName"`
	Kind *int   `json:"RecruitType"`
}

func ctRef(adapter string, v ctJob) (PostingRef, error) {
	if v.ID <= 0 || v.Kind == nil || *v.Kind != 1 || v.Org != ctUnits[adapter].Name {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	id := strconv.FormatInt(v.ID, 10)
	r := PostingRef{ExternalID: id, URL: ctJobURL(adapter, id, v.Name), Title: v.Name, Company: v.Org, JobType: "FULL_TIME", Locations: splitPlaces(v.City)}
	return r, validateRef(r)
}
func (a PublicPlatform) verifyCTCloud(ctx context.Context, s d.Source) error {
	type nav struct {
		Name     string `json:"name"`
		Path     string `json:"path"`
		Params   string `json:"params"`
		Children []struct {
			Name   string `json:"name"`
			Params string `json:"params"`
		} `json:"children"`
	}
	var v ctEnvelope[[]nav]
	if err := a.get(ctx, s, ctOrigin+"/wt/web/platform/mvc/officialWeb/getNavMenu?corpCode=TELE", &v); err != nil {
		return err
	}
	if v.Code != "00" || v.Data == nil {
		return fail("SCHEMA_INVALID", false, 200)
	}
	n := 0
	for _, parent := range v.Data {
		if parent.Name == "校园招聘" && parent.Path == "/" {
			for _, p := range parent.Children {
				var param struct {
					Project string `json:"recruitProject"`
				}
				if json.Unmarshal([]byte(p.Params), &param) == nil && param.Project == ctProject && p.Name == ctProjectName {
					n++
				}
			}
		}
	}
	if n != 1 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	var unit ctEnvelope[struct {
		ID         int64  `json:"uniqueKey"`
		Name       string `json:"cnName"`
		PublicName string `json:"cnExternalName"`
	}]
	q := url.Values{"corpCode": {"TELE"}, "orgId": {ctUnits[s.Adapter].ID}, "recruitType": {"1"}, "recruitProject": {ctProject}}
	if err := a.get(ctx, s, ctOrigin+"/wt/web/platform/mvc/officialWeb/getShowRecruitOrg?"+q.Encode(), &unit); err != nil {
		return err
	}
	if unit.Code != "00" || strconv.FormatInt(unit.Data.ID, 10) != ctUnits[s.Adapter].ID || strings.TrimSuffix(unit.Data.Name, "*") != ctUnits[s.Adapter].Name {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) ctRows(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v ctEnvelope[struct {
			Total *int    `json:"rowCount"`
			Page  int     `json:"rowIndex"`
			Size  int     `json:"rowSize"`
			Jobs  []ctJob `json:"details"`
		}]
		body := url.Values{"rowSize": {"50"}, "rowIndex": {strconv.Itoa(page)}, "recruitType": {"1"}, "org": {ctUnits[s.Adapter].ID}, "postName": {""}, "keyWord": {""}, "workPlace": {""}, "jobCategory": {""}, "recruitProject": {ctProject}}
		if err := a.post(ctx, s, ctOrigin+"/wt/TELE/web/mode400/position/list", body, &v); err != nil {
			return campusPage{}, err
		}
		if v.Code != "00" || v.Data.Total == nil || v.Data.Jobs == nil || v.Data.Page != page || v.Data.Size != 50 {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 50}
		for _, j := range v.Data.Jobs {
			r, err := ctRef(s.Adapter, j)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}
func (a PublicPlatform) discoverCTCloud(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.verifyCTCloud(ctx, s); err != nil {
		return nil, err
	}
	return a.ctRows(ctx, s)
}
func (a PublicPlatform) fetchCTCloud(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != ctJobURL(s.Adapter, r.ExternalID, r.Title) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	// Detail responses do not echo PostId. Recheck the ID in the current
	// unit/project listing, then bind the response by title, unit and project.
	refs, err := a.ctRows(ctx, s)
	if err != nil {
		return "", err
	}
	var actual PostingRef
	for _, ref := range refs {
		if ref.ExternalID == r.ExternalID {
			actual = ref
			break
		}
	}
	if actual.ExternalID == "" || actual.Title != r.Title {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	var v ctEnvelope[struct {
		Name      string `json:"name"`
		Org       string `json:"OrgName"`
		OrgCode   string `json:"orgCode"`
		Project   string `json:"projectName"`
		Duty      string `json:"workConcet"`
		Require   string `json:"serviceCondition"`
		Education string `json:"EducationName"`
		Major     string `json:"Subject"`
	}]
	if err := a.post(ctx, s, ctOrigin+"/wt/TELE/web/mode400/position/detail", url.Values{"postId": {r.ExternalID}, "recruitType": {"1"}}, &v); err != nil {
		return "", err
	}
	unitOK := false
	for _, id := range strings.Split(v.Data.OrgCode, "/") {
		unitOK = unitOK || id == ctUnits[s.Adapter].ID
	}
	if v.Code != "00" || v.Data.Name != actual.Title || v.Data.Org != actual.Company || !unitOK || v.Data.Project != ctProjectName || strings.TrimSpace(plainHTML(v.Data.Require)) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	require := v.Data.Require
	if v.Data.Education != "" {
		require += "\n学历要求：" + v.Data.Education
	}
	if v.Data.Major != "" {
		require += "\n专业要求：" + v.Data.Major
	}
	return campusText(actual, ctProjectName+" · "+actual.Company, v.Data.Duty, require)
}
