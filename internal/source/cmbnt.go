package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

const cmbntOrigin = "https://cmbntjob.cmbchina.com"

type cmbntEnvelope[T any] struct {
	Type string `json:"type"`
	Data T      `json:"data"`
}
type cmbntJob struct {
	ID       string `json:"jobId"`
	Name     string `json:"jobName"`
	Kind     *int   `json:"recruitType"`
	City     string `json:"workingPlace"`
	Duty     string `json:"jobExplain"`
	Require  string `json:"jobCondition"`
	Deadline string `json:"deadline"`
}

func cmbntURL(id string) string {
	return cmbntOrigin + "/pages/socialRecruit/detail.html?jobId=" + id + "&currentType=0"
}
func cmbntRef(v cmbntJob) (PostingRef, error) {
	if !hex32.MatchString(strings.ToLower(v.ID)) || v.ID != strings.ToUpper(v.ID) || v.Kind == nil || *v.Kind != 0 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: v.ID, URL: cmbntURL(v.ID), Title: v.Name, Company: "招银网络科技", JobType: "FULL_TIME", Locations: splitPlaces(v.City)}
	return r, validateRef(r)
}
func (a PublicPlatform) discoverCMBNT(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v cmbntEnvelope[struct {
			Page  int        `json:"pageIndex"`
			Size  int        `json:"pageSize"`
			Total *int       `json:"total"`
			Jobs  []cmbntJob `json:"data"`
		}]
		q := url.Values{"pageIndex": {strconv.Itoa(page - 1)}, "pageSize": {"50"}, "recruitType": {"0"}, "keyWord": {""}, "workingPlace": {""}, "beginDate": {""}}
		if err := a.get(ctx, s, cmbntOrigin+"/api/recruitJob/officialPagedQueryNew?"+q.Encode(), &v); err != nil {
			return campusPage{}, err
		}
		if v.Type != "SUCCESS" || v.Data.Total == nil || v.Data.Jobs == nil || v.Data.Page != page-1 || v.Data.Size != 50 {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 50}
		for _, j := range v.Data.Jobs {
			r, err := cmbntRef(j)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchCMBNT(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !hex32.MatchString(strings.ToLower(r.ExternalID)) || r.ExternalID != strings.ToUpper(r.ExternalID) || r.URL != cmbntURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v cmbntEnvelope[cmbntJob]
	if err := a.get(ctx, s, cmbntOrigin+"/api/recruitJob/officialSelect?jobId="+r.ExternalID, &v); err != nil {
		return "", err
	}
	actual, err := cmbntRef(v.Data)
	if v.Type != "SUCCESS" || err != nil || actual.ExternalID != r.ExternalID || actual.Title != r.Title {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text, err := campusText(actual, "官网应届毕业生分类（不含在职人员、实习生；具体届别以原文为准）", v.Data.Duty, v.Data.Require)
	// 2100 is the platform's unbounded-date sentinel, not a real deadline.
	if err == nil && v.Data.Deadline != "" && !strings.HasPrefix(v.Data.Deadline, "2100-") {
		text += "\n官网截止日期：" + v.Data.Deadline
	}
	return text, err
}
