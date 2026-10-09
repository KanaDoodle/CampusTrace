package source

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// Beisen separates campus (2), social (1) and intern (3). Never infer a cohort
// from a job title: these presets describe the official campus category only.
var beisenCompanies = map[string]struct{ Origin, Company string }{
	"qihoo360":          {"https://360campus.zhiye.com", "360集团"},
	"sany":              {"https://sanycampus.zhiye.com", "三一集团"},
	"inovance":          {"https://inovance.zhiye.com", "汇川技术"},
	"vivo":              {"https://hr-campus.vivo.com", "vivo"},
	"sgm":               {"https://sgm.zhiye.com", "上汽通用/泛亚"},
	"hundsun":           {"https://campus.hundsun.com", "恒生电子"},
	"csc_securities":    {"https://csc108.zhiye.com", "中信建投证券"},
	"guosen_securities": {"https://guosen.zhiye.com", "国信证券"},
	"galaxy_securities": {"https://chinastock.zhiye.com", "中国银河证券"},
	"cicc_securities":   {"https://cicc.zhiye.com", "中金公司"},
	"yuewen":            {"https://yuewen.zhiye.com", "阅文集团"},
	"mthreads":          {"https://mthreads.zhiye.com", "摩尔线程"},
	"nexchip":           {"https://nexchip.zhiye.com", "晶合集成"},
	"neusoft":           {"https://neusoft-campus.zhiye.com", "东软集团"},
	"h3c":               {"https://career.h3c.com", "新华三集团"},
	"yusys":             {"https://yusys-campus.zhiye.com", "宇信科技"},
	"cksic":             {"https://cksic.zhiye.com", "中科芯"},
	"whxmc":             {"https://whxmc.zhiye.com", "新芯股份"},
}

type beisenEnvelope[T any] struct {
	Code  *int `json:"Code"`
	Count *int `json:"Count"`
	Data  T    `json:"Data"`
}
type beisenPost struct {
	ID           string   `json:"Id"`
	JobID        int64    `json:"JobAdId"`
	Name         string   `json:"JobAdName"`
	Category     string   `json:"CategoryId"`
	Status       *int     `json:"Status"`
	Places       []string `json:"LocNames"`
	Kind         string   `json:"Kind"`
	Degree       string   `json:"Degree"`
	Duties       string   `json:"Duty"`
	Requirements string   `json:"Require"`
}

// LocId is the upstream field selector that populates LocNames. Asking for
// LocNames itself silently returns empty places on the modern public portal.
func beisenDisplayFields() []string {
	return []string{"JobAdName", "CategoryId", "Duty", "Require", "LocId", "Kind", "Degree", "Status"}
}

func beisenOK[T any](v beisenEnvelope[T]) error {
	if v.Code != nil && (*v.Code == 401 || *v.Code == 403) {
		return fail("BLOCKED", false, 200)
	}
	if v.Code == nil || *v.Code != 200 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func beisenURL(adapter, id string) string {
	return beisenCompanies[adapter].Origin + "/campus/detail?jobAdId=" + id
}
func beisenRef(adapter string, row beisenPost) (PostingRef, error) {
	company, ok := beisenCompanies[adapter]
	if !ok || !baiduPostID.MatchString(row.ID) || row.JobID <= 0 || row.Category != "2" || row.Status == nil || *row.Status != 1 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	kind := strings.TrimSpace(row.Kind)
	employment := jobType(kind)
	switch kind {
	case "全职":
		employment = "FULL_TIME"
	case "实习", "实习生":
		employment = "INTERNSHIP"
	}
	ref := PostingRef{ExternalID: row.ID, URL: beisenURL(adapter, row.ID), Title: row.Name, Company: company.Company, JobType: employment, Locations: row.Places}
	if isSecuritiesBeisen(adapter) {
		ref.JobType = "UNKNOWN"
	}
	return ref, validateRef(ref)
}
func (a PublicPlatform) discoverBeisen(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v beisenEnvelope[[]beisenPost]
		// Public API PageIndex is zero-based, unlike the page numbers in the UI.
		if err := a.post(ctx, s, beisenCompanies[s.Adapter].Origin+"/api/Jobad/GetJobAdPageList", map[string]any{"category": 2, "PageIndex": page - 1, "PageSize": 50, "Keyword": "", "displayFields": beisenDisplayFields()}, &v); err != nil {
			return campusPage{}, err
		}
		if err := beisenOK(v); err != nil {
			return campusPage{}, err
		}
		if v.Count == nil || v.Data == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Count, Page: page, Size: 50}
		for _, row := range v.Data {
			ref, err := beisenRef(s.Adapter, row)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchBeisen(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !baiduPostID.MatchString(r.ExternalID) || r.URL != beisenURL(s.Adapter, r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	fields, _ := json.Marshal(beisenDisplayFields())
	query := url.Values{"jobAdId": {r.ExternalID}, "category": {"2"}, "displayFields": {string(fields)}}
	var v beisenEnvelope[beisenPost]
	if err := a.get(ctx, s, beisenCompanies[s.Adapter].Origin+"/api/JobAd/GetJobAdInfo?"+query.Encode(), &v); err != nil {
		return "", err
	}
	if err := beisenOK(v); err != nil {
		return "", err
	}
	ref, err := beisenRef(s.Adapter, v.Data)
	if err != nil || ref.ExternalID != r.ExternalID || ref.Title != r.Title {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	scope := "官网校园招聘分类（不混入社招、实习分类；具体毕业年份及用工形式以岗位原文为准）"
	if isSecuritiesBeisen(s.Adapter) {
		scope = "官网校园招聘分类（用工形式、实习考察、毕业范围及经验要求以原文为准）"
	}
	text, err := bankPostingText(ref, scope, v.Data.Duties, v.Data.Requirements)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(v.Data.Kind) != "" {
		text += "\n官网工作性质：" + v.Data.Kind
	}
	if strings.TrimSpace(v.Data.Degree) != "" {
		text += "\n官网学历要求：" + v.Data.Degree
	}
	return text, nil
}
