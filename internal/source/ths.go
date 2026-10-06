package source

import (
	"context"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

const thsOrigin = "https://campus.10jqka.com.cn"
const thsScope = "2027届校园招聘"

type thsEnvelope[T any] struct {
	Code    string `json:"erro_code"`
	Success bool   `json:"success"`
	Data    T      `json:"ex_data"`
}
type thsJob struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Series     int    `json:"apply_recruitment_series_id"`
	SeriesName string `json:"apply_recruitment_series_name"`
	City       string `json:"base"`
	Duty       string `json:"intro"`
	Require    string `json:"requirement"`
}

func thsURL(id string) string { return thsOrigin + "/job/detail?id=" + id }
func thsRef(v thsJob) (PostingRef, error) {
	if v.ID <= 0 || v.Series != 61 || v.SeriesName != thsScope {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	// This series includes conversion internships. The public list does not
	// provide a documented employment type; preserve it as unknown, never
	// relabel every campus role as full-time or infer it from its title.
	r := PostingRef{ExternalID: strconv.FormatInt(v.ID, 10), URL: thsURL(strconv.FormatInt(v.ID, 10)), Title: v.Name, Company: "同花顺", JobType: "UNKNOWN", Locations: splitPlaces(v.City)}
	return r, validateRef(r)
}
func (a PublicPlatform) verifyTHS(ctx context.Context, s d.Source) error {
	var v thsEnvelope[[]struct {
		ID     int    `json:"id"`
		Name   string `json:"series_name"`
		Closed *int   `json:"cut_off"`
	}]
	if err := a.get(ctx, s, thsOrigin+"/api/v3/recruitmentSeries/list?type=0", &v); err != nil {
		return err
	}
	if !v.Success || v.Code != "0" || v.Data == nil {
		return fail("SCHEMA_INVALID", false, 200)
	}
	n := 0
	for _, p := range v.Data {
		if p.ID == 61 {
			if p.Name != thsScope || p.Closed == nil || *p.Closed != 0 {
				return fail("SCHEMA_INVALID", false, 200)
			}
			n++
		}
	}
	if n != 1 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) discoverTHS(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.verifyTHS(ctx, s); err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v thsEnvelope[struct {
			Jobs  []thsJob `json:"apply_show_do_list"`
			Total *int     `json:"total"`
			Page  int      `json:"current"`
			Size  int      `json:"size"`
			Pages int      `json:"pages"`
		}]
		q := url.Values{"page": {strconv.Itoa(page)}, "pageCount": {"50"}, "applyRecruitmentSeriesIds": {"61"}, "type": {"school"}}
		if err := a.get(ctx, s, thsOrigin+"/api/v3/school_recruitment/apply/apply_list?"+q.Encode(), &v); err != nil {
			return campusPage{}, err
		}
		if !v.Success || v.Code != "0" || v.Data.Total == nil || v.Data.Jobs == nil || v.Data.Page != page || v.Data.Size != 50 || v.Data.Pages != (*v.Data.Total+49)/50 {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 50}
		for _, j := range v.Data.Jobs {
			r, err := thsRef(j)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchTHS(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != thsURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	if err := a.verifyTHS(ctx, s); err != nil {
		return "", err
	}
	var v thsEnvelope[thsJob]
	if err := a.get(ctx, s, thsOrigin+"/api/v3/school_recruitment/apply/apply_detail?id="+r.ExternalID, &v); err != nil {
		return "", err
	}
	actual, err := thsRef(v.Data)
	if !v.Success || v.Code != "0" || err != nil || actual.ExternalID != r.ExternalID || actual.Title != r.Title || strings.TrimSpace(plainHTML(v.Data.Duty)) == "" || strings.TrimSpace(plainHTML(v.Data.Require)) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return fmt.Sprintf("岗位名称：%s\n公司：同花顺\n招聘项目：%s（含实习转正岗位；具体用工形式以原文为准）\n工作地点：%s\n岗位职责：\n%s\n任职要求：\n%s", actual.Title, thsScope, strings.Join(actual.Locations, "、"), plainHTML(v.Data.Duty), plainHTML(v.Data.Require)), nil
}
