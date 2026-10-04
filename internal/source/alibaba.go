package source

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const alibabaBatch = int64(100000760001)
const alibabaBatchName = "阿里巴巴2027届应届生"
const alibabaOrigin = "https://campus-talent.alibaba.com"
const alibabaPageSize = 50

type alibabaEnvelope[T any] struct {
	Success bool `json:"success"`
	Relogin bool `json:"relogin"`
	Content T    `json:"content"`
}
type alibabaPost struct {
	ID          int64    `json:"id"`
	BatchID     int64    `json:"batchId"`
	BatchName   string   `json:"batchName"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Category    string   `json:"categoryType"`
	Locations   []string `json:"workLocations"`
	Companies   []string `json:"circleNames"`
	Description string   `json:"description"`
	Requirement string   `json:"requirement"`
	Graduation  struct {
		From int64 `json:"from"`
		To   int64 `json:"to"`
	} `json:"graduationTime"`
}

var alibabaCSRF = regexp.MustCompile(`__token__\s*:\s*"([a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})"`)

// The public page issues an anonymous session and CSRF token, just as for a
// visitor opening the campus page. Each operation gets its own in-memory jar;
// neither browser/user cookies nor these transient tokens enter persistence.
func (a PublicPlatform) alibabaSession(ctx context.Context, s d.Source) (PublicPlatform, string, error) {
	if _, err := PlatformURL(s); err != nil {
		return a, "", err
	}
	if err := a.allowRequest(ctx, s); err != nil {
		return a, "", err
	}
	base := a.Client
	if base == nil {
		base = publicPlatformClient
	}
	client := *base
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != "campus-talent.alibaba.com" || len(via) > 4 {
			return fmt.Errorf("recruiting session redirect outside public origin")
		}
		if base.CheckRedirect != nil {
			return base.CheckRedirect(req, via)
		}
		return nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, AlibabaCampusURL, nil)
	req.Header.Set("User-Agent", "CampusTrace/0.2 (public recruiting source monitoring)")
	resp, err := client.Do(req)
	if err != nil {
		return a, "", fail("TIMEOUT_OR_NETWORK", true, 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 412 {
		return a, "", fail("BLOCKED", false, resp.StatusCode)
	}
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return a, "", fail("HTTP_TRANSIENT", true, resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return a, "", fail("HTTP_ERROR", false, resp.StatusCode)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "text/html" {
		return a, "", fail("SCHEMA_INVALID", false, 200)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return a, "", fail("HTTP_TRANSIENT", true, 200)
	}
	if len(b) > 1<<20 {
		return a, "", fail("RESPONSE_TOO_LARGE", false, 200)
	}
	matches := alibabaCSRF.FindAllSubmatch(b, -1)
	if len(matches) != 1 {
		return a, "", fail("SCHEMA_INVALID", false, 200)
	}
	a.Client = &client
	return a, string(matches[0][1]), nil
}

func alibabaOK[T any](v alibabaEnvelope[T]) error {
	if v.Relogin {
		return fail("BLOCKED", false, 200)
	}
	if !v.Success {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func alibabaQuery(path, token string) string {
	return alibabaOrigin + path + "?" + url.Values{"_csrf": {token}}.Encode()
}
func (a PublicPlatform) alibabaScope(ctx context.Context, s d.Source, token string) error {
	var v alibabaEnvelope[struct {
		Graduate []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"graduate"`
	}]
	if err := a.post(ctx, s, alibabaQuery("/searchCondition/listBatch", token), map[string]any{}, &v); err != nil {
		return err
	}
	if err := alibabaOK(v); err != nil {
		return err
	}
	count := 0
	for _, batch := range v.Content.Graduate {
		if batch.ID == alibabaBatch && batch.Name == alibabaBatchName && batch.Type == "graduate" {
			count++
		}
	}
	if count != 1 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func alibabaRef(v alibabaPost) (PostingRef, error) {
	if v.ID <= 0 || v.BatchID != alibabaBatch || v.BatchName != alibabaBatchName || v.Category != "freshman" || v.Status != "recruit" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: strconv.FormatInt(v.ID, 10), URL: alibabaOrigin + "/campus/position/" + strconv.FormatInt(v.ID, 10), Title: v.Name, Company: "阿里巴巴", JobType: "FULL_TIME", Locations: v.Locations}
	return r, validateRef(r)
}
func (a PublicPlatform) alibabaPage(ctx context.Context, s d.Source, token string, page int) ([]PostingRef, int, error) {
	var v alibabaEnvelope[struct {
		Total       *int          `json:"totalCount"`
		PageSize    int           `json:"pageSize"`
		CurrentPage int           `json:"currentPage"`
		Rows        []alibabaPost `json:"datas"`
	}]
	input := map[string]any{"batchId": alibabaBatch, "pageIndex": page, "pageSize": alibabaPageSize, "channel": "new_campus_group_official_site", "language": "zh"}
	if err := a.post(ctx, s, alibabaQuery("/position/search", token), input, &v); err != nil {
		return nil, 0, err
	}
	if err := alibabaOK(v); err != nil {
		return nil, 0, err
	}
	if v.Content.Total == nil || v.Content.Rows == nil || v.Content.PageSize != alibabaPageSize || v.Content.CurrentPage != page || *v.Content.Total < 0 {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	total := *v.Content.Total
	if total > MaxPostings {
		return nil, 0, fail("CAPACITY", false, 200)
	}
	if len(v.Content.Rows) != min(alibabaPageSize, max(0, total-(page-1)*alibabaPageSize)) {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	refs := []PostingRef{}
	for _, row := range v.Content.Rows {
		ref, err := alibabaRef(row)
		if err != nil {
			return nil, 0, err
		}
		refs = append(refs, ref)
	}
	return refs, total, nil
}
func (a PublicPlatform) previewAlibaba(ctx context.Context, site CampusSite) (CampusPreview, error) {
	s := d.Source{ID: "alibaba-preview", Adapter: "alibaba", Tenant: "100000760001", RateLimit: 30}
	a, token, err := a.alibabaSession(ctx, s)
	if err != nil {
		return CampusPreview{}, err
	}
	if err = a.alibabaScope(ctx, s, token); err != nil {
		return CampusPreview{}, err
	}
	refs, total, err := a.alibabaPage(ctx, s, token, 1)
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: s.Tenant, Total: total, Samples: refs[:min(5, len(refs))]}, err
}
func (a PublicPlatform) discoverAlibaba(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	a, token, err := a.alibabaSession(ctx, s)
	if err != nil {
		return nil, err
	}
	if err = a.alibabaScope(ctx, s, token); err != nil {
		return nil, err
	}
	refs, total, err := a.alibabaPage(ctx, s, token, 1)
	if err != nil {
		return nil, err
	}
	for page := 2; page <= (total+alibabaPageSize-1)/alibabaPageSize; page++ {
		rows, count, err := a.alibabaPage(ctx, s, token, page)
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
func (a PublicPlatform) fetchAlibaba(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !xhsID.MatchString(r.ExternalID) || r.URL != alibabaOrigin+"/campus/position/"+r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	a, token, err := a.alibabaSession(ctx, s)
	if err != nil {
		return "", err
	}
	var v alibabaEnvelope[alibabaPost]
	input := map[string]any{"id": r.ExternalID, "channel": "new_campus_group_official_site", "language": "zh"}
	if err = a.post(ctx, s, alibabaQuery("/position/detail", token), input, &v); err != nil {
		return "", err
	}
	if err = alibabaOK(v); err != nil {
		return "", err
	}
	ref, err := alibabaRef(v.Content)
	if err != nil || ref.ExternalID != r.ExternalID || strings.TrimSpace(v.Content.Description) == "" || strings.TrimSpace(v.Content.Requirement) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	row := v.Content
	text := fmt.Sprintf("岗位名称：%s\n招聘项目：%s\n官网列出的业务集团：%s\n工作地点：%s\n工作职责：\n%s\n任职资格：\n%s", row.Name, row.BatchName, strings.Join(row.Companies, "、"), strings.Join(row.Locations, "、"), plainHTML(row.Description), plainHTML(row.Requirement))
	if row.Graduation.From > 0 && row.Graduation.To >= row.Graduation.From {
		zone := time.FixedZone("Asia/Shanghai", 8*3600)
		text += fmt.Sprintf("\n毕业时间范围：%s 至 %s", time.UnixMilli(row.Graduation.From).In(zone).Format("2006年01月02日"), time.UnixMilli(row.Graduation.To).In(zone).Format("2006年01月02日"))
	}
	return text, nil
}
