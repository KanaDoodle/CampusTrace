package source

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const ctripOrigin = "https://careers.ctrip.com"
const ctripJobAPI = ctripOrigin + "/api/hrrecruit/getJobAd"

var ctripJobID = regexp.MustCompile(`^MJ[0-9]{6,10}$`)

type ctripEnvelope struct {
	Code  string `json:"retCode"`
	Value struct {
		Total *int       `json:"total"`
		Jobs  []ctripJob `json:"recruitJobAdList"`
	} `json:"retValue"`
}

type ctripJob struct {
	ID          string `json:"fromId"`
	Title       string `json:"jobTitle"`
	Kind        string `json:"kind"`
	City        string `json:"cityName"`
	Description string `json:"requirements"`
}

func ctripURL(id string) string { return ctripOrigin + "/campus/job-detail/" + id }

func ctripRef(job ctripJob) (PostingRef, error) {
	if !ctripJobID.MatchString(job.ID) || job.Kind != "1" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	ref := PostingRef{
		ExternalID: job.ID, URL: ctripURL(job.ID), Title: strings.TrimSpace(job.Title),
		Company: "携程/Trip.com", JobType: "FULL_TIME", Locations: splitPlaces(job.City),
	}
	return ref, validateRef(ref)
}

func ctripListCondition() map[string]any {
	// These fields and category=2 are sent by the public campus job list.
	empty := []string{}
	return map[string]any{
		"fromId": empty, "keyword": "", "kind": empty, "country": empty,
		"city": empty, "bucode": empty, "jobFamilyCode": empty,
		"category": 2, "jobFamilyGroupCode": empty,
	}
}

func (a PublicPlatform) discoverCtrip(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v ctripEnvelope
		body := map[string]any{
			"condition": ctripListCondition(),
			"pager":     map[string]string{"index": fmt.Sprint(page), "size": "10"},
		}
		if err := a.post(ctx, s, ctripJobAPI, body, &v); err != nil {
			return campusPage{}, err
		}
		if v.Code != "201" || v.Value.Total == nil || v.Value.Jobs == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Value.Total, Page: page, Size: 10}
		for _, job := range v.Value.Jobs {
			ref, err := ctripRef(job)
			if err != nil {
				return campusPage{}, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}

func (a PublicPlatform) fetchCtrip(ctx context.Context, s d.Source, ref PostingRef) (string, error) {
	if !ctripJobID.MatchString(ref.ExternalID) || ref.URL != ctripURL(ref.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	body := map[string]any{
		"condition": map[string]any{"fromId": []string{ref.ExternalID}},
		"pager":     map[string]string{"index": "1", "size": "10"},
	}
	var v ctripEnvelope
	if err := a.post(ctx, s, ctripJobAPI, body, &v); err != nil {
		return "", err
	}
	if v.Code != "201" || v.Value.Total == nil || *v.Value.Total != 1 || len(v.Value.Jobs) != 1 {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	job := v.Value.Jobs[0]
	actual, err := ctripRef(job)
	if err != nil || actual.ExternalID != ref.ExternalID || actual.Title != ref.Title {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	description := strings.TrimSpace(plainHTML(job.Description))
	if description == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return fmt.Sprintf("岗位名称：%s\n公司：%s\n招聘类型：应届生全职\n招聘范围：官网校园招聘分类 Fresh Graduates\n工作地点：%s\n岗位原文：\n%s", actual.Title, actual.Company, strings.Join(actual.Locations, "、"), description), nil
}
