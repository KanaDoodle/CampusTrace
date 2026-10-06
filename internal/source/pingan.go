package source

import (
	"context"
	"net/url"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const pinganOrigin = "https://campus.pingan.com"
const pinganAPI = pinganOrigin + "/zztj-recruit-talent-webserver/rctt"
const pinganGroup = "6c1db1bba8c33deab19a733ec785711a"

var pinganUnits = map[string]struct{ ID, Name string }{"pingan_tech": {"PA011", "平安科技"}, "pingan_oneconnect": {"PA038", "金融壹账通"}, "pingan_wallet": {"PA027", "平安壹钱包"}}

func pinganCampusURL(adapter string) string {
	return pinganOrigin + "/freshGraduates?" + url.Values{"id": {pinganUnits[adapter].ID}, "type": {"company"}}.Encode()
}
func pinganJobURL(id string) string { return pinganOrigin + "/positionDetail?positionId=" + id }

type pinganEnvelope[T any] struct {
	Code string `json:"responseCode"`
	Data T      `json:"data"`
}
type pinganJob struct {
	ID          string `json:"idPosition"`
	DetailID    string `json:"positionId"`
	Name        string `json:"positionName"`
	Unit        string `json:"businessUnitId"`
	UnitName    string `json:"businessUnitName"`
	Kind        string `json:"positionType"`
	RecruitType string `json:"positionTypes"`
	Published   string `json:"publishStatus"`
	City        string `json:"workCity"`
	Duty        string `json:"duty"`
	Require     string `json:"qualification"`
	Education   string `json:"education"`
	Department  string `json:"deptShowName"`
}

func pinganRef(adapter string, j pinganJob) (PostingRef, error) {
	unit := pinganUnits[adapter]
	if !hex32.MatchString(j.ID) || j.Unit != unit.ID || j.UnitName != unit.Name || j.Published != "P" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: j.ID, URL: pinganJobURL(j.ID), Title: j.Name, Company: unit.Name, JobType: "FULL_TIME", Locations: splitPlaces(j.City)}
	return r, validateRef(r)
}
func (a PublicPlatform) verifyPingan(ctx context.Context, s d.Source) error {
	var group pinganEnvelope[string]
	if err := a.post(ctx, s, pinganAPI+"/candidate/officialWebsite/selectGroupOfficial", map[string]any{"websiteType": 3}, &group); err != nil {
		return err
	}
	if group.Code != "10001" || group.Data != pinganGroup {
		return fail("SCHEMA_INVALID", false, 200)
	}
	var filters pinganEnvelope[struct {
		Companies struct {
			Data map[string][]struct {
				ID   string `json:"businessUnitId"`
				Name string `json:"companyName"`
			} `json:"data"`
		} `json:"campusCompanyMap"`
	}]
	if err := a.post(ctx, s, pinganAPI+"/candidate/position/campus/positionSearch/queryCityCompanyCategory", map[string]any{"wecruitId": pinganGroup, "positionType": "1"}, &filters); err != nil {
		return err
	}
	matches := 0
	for _, group := range filters.Data.Companies.Data {
		for _, company := range group {
			if company.ID == pinganUnits[s.Adapter].ID && company.Name == pinganUnits[s.Adapter].Name {
				matches++
			}
		}
	}
	if filters.Code != "10001" || matches != 1 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) discoverPingan(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.verifyPingan(ctx, s); err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v pinganEnvelope[struct {
			Jobs  []pinganJob `json:"list"`
			Page  int         `json:"pageNo"`
			Size  int         `json:"pageSize"`
			Total *int        `json:"totalCount"`
			Pages *int        `json:"totalPage"`
		}]
		body := map[string]any{"PageNum": page, "pageSize": 50, "wecruitId": pinganGroup, "positionType": "1", "wecruitPlatform": true, "businessUnitId": pinganUnits[s.Adapter].ID, "keyWord": "", "positionCategoryId": "", "workCity": "", "interviewCity": ""}
		if err := a.post(ctx, s, pinganAPI+"/candidate/position/campus/positionSearch/queryPositionPage", body, &v); err != nil {
			return campusPage{}, err
		}
		if v.Code != "10001" || v.Data.Total == nil || v.Data.Pages == nil || *v.Data.Total < 0 || *v.Data.Pages != (*v.Data.Total+49)/50 || v.Data.Page != page || v.Data.Size != 50 || v.Data.Jobs == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 50}
		for _, j := range v.Data.Jobs {
			if j.Kind != "全职" {
				return out, fail("SCHEMA_INVALID", false, 200)
			}
			r, err := pinganRef(s.Adapter, j)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchPingan(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !hex32.MatchString(r.ExternalID) || r.URL != pinganJobURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v pinganEnvelope[struct {
		Position pinganJob `json:"position"`
	}]
	if err := a.post(ctx, s, pinganAPI+"/candidate/position/campus/positionSearch/queryPositionDetail", map[string]any{"wecruitId": pinganGroup, "positionId": r.ExternalID, "wecruitPlatform": true}, &v); err != nil {
		return "", err
	}
	j := v.Data.Position
	actual, err := pinganRef(s.Adapter, j)
	if err != nil {
		return "", err
	}
	if v.Code != "10001" || j.ID != r.ExternalID || j.DetailID != r.ExternalID || j.Name != r.Title || j.RecruitType != "1" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text, err := campusText(actual, "官网应届生分类 · "+pinganUnits[s.Adapter].Name, j.Duty, j.Require)
	if err != nil {
		return "", err
	}
	if j.Education != "" {
		text += "\n学历（官网原文）：" + plainHTML(j.Education)
	}
	if j.Department != "" {
		text += "\n部门（官网原文）：" + plainHTML(j.Department)
	}
	return text, nil
}
