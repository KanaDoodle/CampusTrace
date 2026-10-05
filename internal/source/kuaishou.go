package source

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const kuaishouOrigin = "https://campus.kuaishou.cn"
const kuaishouBase = kuaishouOrigin + "/recruit/campus/e"
const kuaishouProjectCode = "20271779425607"
const kuaishouPageSize = 50

type kuaishouEnvelope[T any] struct {
	Code   *int `json:"code"`
	Result T    `json:"result"`
}
type kuaishouProject struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Year         string `json:"year"`
	Type         string `json:"projectType"`
	Active       *bool  `json:"active"`
	GraduateFrom int64  `json:"graduateStartTime"`
	GraduateTo   int64  `json:"graduateEndTime"`
}
type kuaishouPost struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Project     string `json:"recruitProjectCode"`
	SubProject  string `json:"recruitSubProjectCode"`
	Nature      string `json:"positionNatureCode"`
	Status      string `json:"positionStatusCode"`
	Visible     *bool  `json:"ifShowRecruitWebsite"`
	Description string `json:"description"`
	Requirement string `json:"positionDemand"`
	Locations   []struct {
		Name string `json:"name"`
	} `json:"workLocationDicts"`
}

func kuaishouOK[T any](v kuaishouEnvelope[T]) error {
	if v.Code != nil && (*v.Code == 401 || *v.Code == 403 || *v.Code == 412) {
		return fail("BLOCKED", false, 200)
	}
	if v.Code == nil || *v.Code != 0 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}

// These endpoints need no login or token. Reuse the public transport while
// excluding any caller cookie jar and refusing redirects outside this origin.
func (a PublicPlatform) kuaishouPublic(s d.Source) (PublicPlatform, error) {
	if _, err := PlatformURL(s); err != nil {
		return a, err
	}
	base := a.Client
	if base == nil {
		base = publicPlatformClient
	}
	client := *base
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != "campus.kuaishou.cn" || len(via) > 4 {
			return fmt.Errorf("public recruiting redirect outside origin")
		}
		if base.CheckRedirect != nil {
			return base.CheckRedirect(req, via)
		}
		return nil
	}
	a.Client = &client
	return a, nil
}
func (a PublicPlatform) kuaishouScope(ctx context.Context, s d.Source, positionID string) (kuaishouProject, error) {
	raw := kuaishouBase + "/api/v1/open/sub-project/findByCode?code=" + kuaishouProjectCode
	if positionID != "" {
		raw += "&positionId=" + url.QueryEscape(positionID)
	}
	var v kuaishouEnvelope[kuaishouProject]
	if err := a.get(ctx, s, raw, &v); err != nil {
		return v.Result, err
	}
	if err := kuaishouOK(v); err != nil {
		return v.Result, err
	}
	p := v.Result
	if p.Code != kuaishouProjectCode || p.Name != "2027应届生" || p.Year != "2027" || p.Type != "fulltime" || p.Active == nil || !*p.Active || ((p.GraduateFrom != 0 || p.GraduateTo != 0) && (p.GraduateFrom <= 0 || p.GraduateTo < p.GraduateFrom)) {
		return p, fail("SCHEMA_INVALID", false, 200)
	}
	return p, nil
}
func kuaishouRef(p kuaishouPost) (PostingRef, error) {
	if p.ID <= 0 || p.Project != "schoolr" || p.SubProject != kuaishouProjectCode || p.Nature != "fulltime" || p.Status != "Release" || p.Visible == nil || !*p.Visible {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	locations := []string{}
	seen := map[string]bool{}
	for _, location := range p.Locations {
		name := strings.TrimSpace(location.Name)
		if name != "" && !seen[name] {
			locations, seen[name] = append(locations, name), true
		}
	}
	id := strconv.FormatInt(p.ID, 10)
	r := PostingRef{ExternalID: id, URL: kuaishouBase + "/#/campus/job-info/" + id, Title: p.Name, Company: "快手", JobType: "FULL_TIME", Locations: locations}
	return r, validateRef(r)
}
func (a PublicPlatform) kuaishouPage(ctx context.Context, s d.Source, page int) ([]PostingRef, int, error) {
	var v kuaishouEnvelope[struct {
		Total *int           `json:"total"`
		Page  int            `json:"pageNum"`
		Size  int            `json:"pageSize"`
		Pages int            `json:"pages"`
		Rows  []kuaishouPost `json:"list"`
	}]
	input := map[string]any{"pageNum": page, "pageSize": kuaishouPageSize, "recruitSubProjectCodes": []string{kuaishouProjectCode}}
	if err := a.post(ctx, s, kuaishouBase+"/api/v1/open/positions/simple", input, &v); err != nil {
		return nil, 0, err
	}
	if err := kuaishouOK(v); err != nil {
		return nil, 0, err
	}
	if v.Result.Total == nil || *v.Result.Total < 0 || v.Result.Rows == nil {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	total := *v.Result.Total
	if total > MaxPostings {
		return nil, 0, fail("CAPACITY", false, 200)
	}
	if v.Result.Page != page || v.Result.Size != kuaishouPageSize || v.Result.Pages != (total+kuaishouPageSize-1)/kuaishouPageSize || len(v.Result.Rows) != min(kuaishouPageSize, max(0, total-(page-1)*kuaishouPageSize)) {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	refs := []PostingRef{}
	for _, row := range v.Result.Rows {
		ref, err := kuaishouRef(row)
		if err != nil {
			return nil, 0, err
		}
		refs = append(refs, ref)
	}
	return refs, total, nil
}
func (a PublicPlatform) previewKuaishou(ctx context.Context, site CampusSite) (CampusPreview, error) {
	s := d.Source{ID: "kuaishou-preview", Adapter: "kuaishou", Tenant: kuaishouProjectCode, RateLimit: 30}
	a, err := a.kuaishouPublic(s)
	if err != nil {
		return CampusPreview{}, err
	}
	if _, err := a.kuaishouScope(ctx, s, ""); err != nil {
		return CampusPreview{}, err
	}
	refs, total, err := a.kuaishouPage(ctx, s, 1)
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: s.Tenant, Total: total, Samples: refs[:min(5, len(refs))]}, err
}
func (a PublicPlatform) discoverKuaishou(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	a, err := a.kuaishouPublic(s)
	if err != nil {
		return nil, err
	}
	if _, err := a.kuaishouScope(ctx, s, ""); err != nil {
		return nil, err
	}
	refs, total, err := a.kuaishouPage(ctx, s, 1)
	if err != nil {
		return nil, err
	}
	for page := 2; page <= (total+kuaishouPageSize-1)/kuaishouPageSize; page++ {
		rows, count, err := a.kuaishouPage(ctx, s, page)
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
func (a PublicPlatform) fetchKuaishou(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !xhsID.MatchString(r.ExternalID) || r.URL != kuaishouBase+"/#/campus/job-info/"+r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	a, err := a.kuaishouPublic(s)
	if err != nil {
		return "", err
	}
	var v kuaishouEnvelope[kuaishouPost]
	if err := a.get(ctx, s, kuaishouBase+"/api/v1/open/positions/find?id="+r.ExternalID, &v); err != nil {
		return "", err
	}
	if err := kuaishouOK(v); err != nil {
		return "", err
	}
	ref, err := kuaishouRef(v.Result)
	row := v.Result
	duties, requirements := plainHTML(row.Description), plainHTML(row.Requirement)
	if err != nil || ref.ExternalID != r.ExternalID || strings.TrimSpace(duties) == "" || strings.TrimSpace(requirements) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	project, err := a.kuaishouScope(ctx, s, r.ExternalID)
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("岗位名称：%s\n招聘范围：快手 · %s\n工作地点：%s\n岗位职责：\n%s\n任职要求：\n%s", row.Name, project.Name, strings.Join(ref.Locations, "、"), duties, requirements)
	if project.GraduateFrom > 0 {
		zone := time.FixedZone("Asia/Shanghai", 8*60*60)
		text += fmt.Sprintf("\n官网公开毕业时间范围：%s 至 %s", time.UnixMilli(project.GraduateFrom).In(zone).Format("2006年01月02日"), time.UnixMilli(project.GraduateTo).In(zone).Format("2006年01月02日"))
	}
	return text, nil
}
