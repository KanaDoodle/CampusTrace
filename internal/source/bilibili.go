package source

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const bilibiliOrigin = "https://jobs.bilibili.com"

type bilibiliEnvelope[T any] struct {
	Code *int `json:"code"`
	Data T    `json:"data"`
}
type bilibiliPost struct {
	ID           int64  `json:"id"`
	ProjectID    int64  `json:"campusProjectId"`
	Name         string `json:"positionName"`
	RecruitType  int    `json:"recruitType"`
	Type         string `json:"positionType"`
	TypeName     string `json:"positionTypeName"`
	Place        string `json:"workLocation"`
	City         string `json:"workCity"`
	Description  string `json:"positionDescription"`
	Descriptions string `json:"positionDescriptions"`
	ApplyStart   string `json:"webApplyStartTime"`
	ApplyEnd     string `json:"webApplyEndTime"`
	GraduateFrom string `json:"graduationStartTime"`
	GraduateTo   string `json:"graduationEndTime"`
}

func bilibiliOK[T any](v bilibiliEnvelope[T]) error {
	if v.Code != nil && (*v.Code == -101 || *v.Code == -111 || *v.Code == 401 || *v.Code == 403 || *v.Code == 412) {
		return fail("BLOCKED", false, 200)
	}
	if v.Code == nil || *v.Code != 0 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}

// The official client initializes an anonymous (X-UserType=2) visitor through
// this public endpoint. Copy neither browser cookies nor candidate credentials;
// keep newly issued cookies and X-CSRF within this one operation. Never cache
// the token response, even if the server advertises an HTTP validator.
func (a PublicPlatform) bilibiliSession(ctx context.Context, s d.Source) (PublicPlatform, error) {
	if _, err := PlatformURL(s); err != nil {
		return a, err
	}
	base := a.Client
	if base == nil {
		base = publicPlatformClient
	}
	client := *base
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != "jobs.bilibili.com" || len(via) > 4 {
			return fmt.Errorf("recruiting session redirect outside public origin")
		}
		if base.CheckRedirect != nil {
			return base.CheckRedirect(req, via)
		}
		return nil
	}
	a.Client, a.bilibiliCSRF = &client, ""
	bootstrap := a
	bootstrap.CacheRead, bootstrap.CacheWrite = nil, nil
	var v bilibiliEnvelope[string]
	if err := bootstrap.get(ctx, s, bilibiliOrigin+"/api/auth/v1/csrf/token", &v); err != nil {
		return a, err
	}
	if err := bilibiliOK(v); err != nil {
		return a, err
	}
	if len(v.Data) < 1 || len(v.Data) > 2048 || strings.ContainsAny(v.Data, "\r\n") {
		return a, fail("SCHEMA_INVALID", false, 200)
	}
	a.bilibiliCSRF = v.Data
	return a, nil
}
func bilibiliRef(v bilibiliPost) (PostingRef, error) {
	// The public UI's Freshmen enum is "3", Intern is "0". The list has no
	// positionType field, so verify its published type name as well as the
	// campus recruitType. Never import names suppressed by the official UI.
	if v.ID <= 0 || v.ProjectID <= 0 || v.RecruitType != 1 || v.TypeName != "全职" || strings.Contains(v.Name, "#012830") {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	place := v.City
	if place == "" {
		place = v.Place
	}
	r := PostingRef{ExternalID: strconv.FormatInt(v.ID, 10), URL: bilibiliOrigin + "/campus/positions/" + strconv.FormatInt(v.ID, 10), Title: v.Name, Company: "哔哩哔哩", JobType: "FULL_TIME", Locations: splitXHSLocations(place)}
	return r, validateRef(r)
}
func (a PublicPlatform) bilibiliPage(ctx context.Context, s d.Source, page int) ([]PostingRef, int, error) {
	var v bilibiliEnvelope[struct {
		Total *int           `json:"total"`
		Rows  []bilibiliPost `json:"list"`
	}]
	input := map[string]any{"pageSize": 10, "pageNum": page, "positionName": "", "postCode": []string{}, "postCodeList": []string{}, "workLocationList": []string{}, "workTypeList": []string{"3"}, "positionTypeList": []string{"3"}, "deptCodeList": []string{}, "practiceTypes": []string{}, "recruitType": 1, "onlyHotRecruit": 0}
	if err := a.post(ctx, s, bilibiliOrigin+"/api/campus/position/positionList", input, &v); err != nil {
		return nil, 0, err
	}
	if err := bilibiliOK(v); err != nil {
		return nil, 0, err
	}
	// The API calculates pages using this page's row count: the 9-row last
	// page of 89 jobs reports size=9, pages=10. Derive pagination from total
	// and the requested page size, then verify the exact expected row count.
	if v.Data.Total == nil || *v.Data.Total < 0 || v.Data.Rows == nil {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	total := *v.Data.Total
	if total > MaxPostings {
		return nil, 0, fail("CAPACITY", false, 200)
	}
	if len(v.Data.Rows) != min(10, max(0, total-(page-1)*10)) {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	refs := []PostingRef{}
	for _, row := range v.Data.Rows {
		ref, err := bilibiliRef(row)
		if err != nil {
			return nil, 0, err
		}
		refs = append(refs, ref)
	}
	return refs, total, nil
}
func (a PublicPlatform) previewBilibili(ctx context.Context, site CampusSite) (CampusPreview, error) {
	s := d.Source{ID: "bilibili-preview", Adapter: "bilibili", Tenant: "freshmen", RateLimit: 30}
	a, err := a.bilibiliSession(ctx, s)
	if err != nil {
		return CampusPreview{}, err
	}
	refs, total, err := a.bilibiliPage(ctx, s, 1)
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: s.Tenant, Total: total, Samples: refs[:min(5, len(refs))]}, err
}
func (a PublicPlatform) discoverBilibili(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	a, err := a.bilibiliSession(ctx, s)
	if err != nil {
		return nil, err
	}
	refs, total, err := a.bilibiliPage(ctx, s, 1)
	if err != nil {
		return nil, err
	}
	for page := 2; page <= (total+9)/10; page++ {
		rows, count, err := a.bilibiliPage(ctx, s, page)
		if err != nil {
			return nil, err
		}
		if count != total {
			return nil, fail("SCHEMA_INVALID", false, 200)
		}
		refs = append(refs, rows...)
	}
	return refs, nil
}
func (a PublicPlatform) fetchBilibili(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !xhsID.MatchString(r.ExternalID) || r.URL != bilibiliOrigin+"/campus/positions/"+r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	a, err := a.bilibiliSession(ctx, s)
	if err != nil {
		return "", err
	}
	var v bilibiliEnvelope[bilibiliPost]
	if err := a.get(ctx, s, bilibiliOrigin+"/api/campus/position/detail/"+r.ExternalID, &v); err != nil {
		return "", err
	}
	if err := bilibiliOK(v); err != nil {
		return "", err
	}
	ref, err := bilibiliRef(v.Data)
	row := v.Data
	if row.Description == "" {
		row.Description = row.Descriptions
	}
	if err != nil || ref.ExternalID != r.ExternalID || row.Type != "3" || strings.TrimSpace(row.Description) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text := fmt.Sprintf("岗位名称：%s\n招聘范围：哔哩哔哩官网应届生校招\n工作地点：%s\n岗位描述：\n%s", row.Name, strings.Join(ref.Locations, "、"), plainHTML(row.Description))
	for _, pair := range [][2]string{{"网申开始日期", row.ApplyStart}, {"网申截止日期", row.ApplyEnd}, {"毕业范围开始日期", row.GraduateFrom}, {"毕业范围结束日期", row.GraduateTo}} {
		if pair[1] != "" {
			text += "\n" + pair[0] + "：" + pair[1]
		}
	}
	return text, nil
}
