package source

import (
	"context"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"strings"
)

const bydOrigin = "https://job.byd.com"
const bydAPI = bydOrigin + "/portal/api/portal-api"

type bydEnvelope[T any] struct {
	Code *int  `json:"code"`
	OK   *bool `json:"oK"`
	Data T     `json:"data"`
	Page struct {
		Index int  `json:"pageIndex"`
		Size  int  `json:"pageSize"`
		Total *int `json:"totalCount"`
		Pages int  `json:"totalPage"`
	} `json:"page"`
}
type bydEntry struct {
	Name    string `json:"postEntryName"`
	Topic   string `json:"schoolTopic"`
	Batch   string `json:"batch"`
	Nature  string `json:"campusNature"`
	Abroad  string `json:"abroad"`
	Degree  string `json:"degree"`
	Status  string `json:"status"`
	Content string `json:"content"`
}
type bydPost struct {
	ID      string `json:"id"`
	Name    string `json:"jobName"`
	Batch   int    `json:"batch"`
	Nature  string `json:"campusNature"`
	Place   string `json:"workPlace"`
	Details []struct {
		Abroad       string `json:"abroad"`
		Department   string `json:"division"`
		Direction    string `json:"researchDirection"`
		Place        string `json:"workPlace"`
		Duties       string `json:"jobDuty"`
		Requirements string `json:"jobRequirements"`
	} `json:"positionInfoList"`
}

func bydOK[T any](v bydEnvelope[T]) error {
	if v.Code == nil || *v.Code != 0 || v.OK == nil || !*v.OK {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) bydScope(ctx context.Context, s d.Source) (bydEntry, error) {
	var v bydEnvelope[[]bydEntry]
	if err := a.get(ctx, s, bydAPI+"/postEntryConfig/list", &v); err != nil {
		return bydEntry{}, err
	}
	if err := bydOK(v); err != nil {
		return bydEntry{}, err
	}
	var result bydEntry
	count := 0
	for _, p := range v.Data {
		if p.Name == "应届生" {
			result = p
			count++
		}
	}
	if count != 1 || result.Topic != bydProject || result.Batch != "2027" || result.Nature != "008501" || result.Abroad != "00112" || result.Degree != "" || result.Status != "00111" || strings.TrimSpace(result.Content) == "" {
		return result, fail("SCHEMA_INVALID", false, 200)
	}
	return result, nil
}
func bydURL(id string) string {
	return bydOrigin + "/portal/mobile/schoolPositionDetail?id=" + id + "&abroad=00112"
}
func bydRef(row bydPost) (PostingRef, error) {
	if !numericID(row.ID) || row.Batch != 2027 || row.Nature != "008501" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	ref := PostingRef{ExternalID: row.ID, URL: bydURL(row.ID), Title: row.Name, Company: "比亚迪", JobType: "FULL_TIME", Locations: splitPlaces(row.Place)}
	return ref, validateRef(ref)
}
func (a PublicPlatform) discoverBYD(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if _, err := a.bydScope(ctx, s); err != nil {
		return nil, err
	}
	// The official endpoint has no stable secondary sort across pages. A
	// bounded single-page read avoids missing jobs when tied rows move between
	// pages. Still reject totals above the existing 500-job source limit.
	var v bydEnvelope[[]bydPost]
	input := map[string]any{"topicCode": bydProject, "batch": "2027", "campusNature": "008501", "abroad": "00112", "degree": "", "jobType": []any{}, "researchDirection": []any{}, "workPlace": []any{}, "keywords": "", "pageSize": MaxPostings, "pageIndex": 1}
	if err := a.post(ctx, s, bydAPI+"/schoolPortal/queryPositionList", input, &v); err != nil {
		return nil, err
	}
	if err := bydOK(v); err != nil {
		return nil, err
	}
	if v.Page.Total == nil || *v.Page.Total < 0 || v.Data == nil {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	if *v.Page.Total > MaxPostings {
		return nil, fail("CAPACITY", false, 200)
	}
	if v.Page.Index != 1 || v.Page.Size != MaxPostings || v.Page.Pages != (*v.Page.Total+MaxPostings-1)/MaxPostings || len(v.Data) != *v.Page.Total {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	refs := []PostingRef{}
	for _, row := range v.Data {
		ref, err := bydRef(row)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}
func (a PublicPlatform) fetchBYD(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != bydURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	scope, err := a.bydScope(ctx, s)
	if err != nil {
		return "", err
	}
	var v bydEnvelope[bydPost]
	if err := a.get(ctx, s, bydAPI+"/schoolPortal/queryPosition?id="+r.ExternalID+"&abroad=00112&degree=", &v); err != nil {
		return "", err
	}
	if err := bydOK(v); err != nil {
		return "", err
	}
	ref, err := bydRef(v.Data)
	if err != nil || ref.ExternalID != r.ExternalID || len(v.Data.Details) == 0 {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	parts := []string{fmt.Sprintf("岗位名称：%s\n公司：比亚迪\n招聘类型：应届生全职\n招聘范围：2027应届生（不含实习、外派专项）\n官网毕业范围：%s\n工作地点：%s", ref.Title, scope.Content, strings.Join(ref.Locations, "、"))}
	for _, p := range v.Data.Details {
		if p.Abroad != "00112" || strings.TrimSpace(plainHTML(p.Duties)) == "" || strings.TrimSpace(plainHTML(p.Requirements)) == "" {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		parts = append(parts, fmt.Sprintf("部门与方向：%s / %s\n工作地点：%s\n岗位职责：\n%s\n任职要求：\n%s", p.Department, p.Direction, p.Place, plainHTML(p.Duties), plainHTML(p.Requirements)))
	}
	return strings.Join(parts, "\n\n"), nil
}
