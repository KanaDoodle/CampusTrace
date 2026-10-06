package source

import (
	"context"
	"encoding/json"
	"net/url"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// Beisen separates campus (2), social (1) and intern (3). Never infer a cohort
// from a job title: these presets describe the official campus category only.
var beisenCompanies = map[string]struct{ Origin, Company string }{
	"qihoo360": {"https://360campus.zhiye.com", "360集团"},
	"sany":     {"https://sanycampus.zhiye.com", "三一集团"},
	"inovance": {"https://inovance.zhiye.com", "汇川技术"},
	"vivo":     {"https://hr-campus.vivo.com", "vivo"},
	"sgm":      {"https://sgm.zhiye.com", "上汽通用/泛亚"},
	"hundsun":  {"https://campus.hundsun.com", "恒生电子"},
	"yuewen":   {"https://yuewen.zhiye.com", "阅文集团"},
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
	Duties       string   `json:"Duty"`
	Requirements string   `json:"Require"`
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
	ref := PostingRef{ExternalID: row.ID, URL: beisenURL(adapter, row.ID), Title: row.Name, Company: company.Company, JobType: "FULL_TIME", Locations: row.Places}
	return ref, validateRef(ref)
}
func (a PublicPlatform) discoverBeisen(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v beisenEnvelope[[]beisenPost]
		// Public API PageIndex is zero-based, unlike the page numbers in the UI.
		if err := a.post(ctx, s, beisenCompanies[s.Adapter].Origin+"/api/Jobad/GetJobAdPageList", map[string]any{"category": 2, "PageIndex": page - 1, "PageSize": 50, "Keyword": ""}, &v); err != nil {
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
	fields, _ := json.Marshal([]string{"JobAdName", "CategoryId", "Duty", "Require", "LocNames", "Status"})
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
	return campusText(ref, "官网校园招聘分类（不含社招、实习；具体毕业年份以岗位原文为准）", v.Data.Duties, v.Data.Requirements)
}
