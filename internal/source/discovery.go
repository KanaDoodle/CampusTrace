package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"golang.org/x/net/html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const MaxPostings = 500

// Capabilities are additive. Existing manual Adapter.Fetch is unchanged.
type PostingRef struct {
	ExternalID string   `json:"external_id"`
	URL        string   `json:"url"`
	Title      string   `json:"title"`
	Company    string   `json:"company"`
	JobType    string   `json:"job_type"`
	Locations  []string `json:"locations"`
}
type PostingDiscoverer interface {
	Discover(context.Context, d.Source, d.WatchTarget) ([]PostingRef, error)
}
type PostingFetcher interface {
	FetchPosting(context.Context, d.Source, PostingRef) (Result, error)
}
type DiscoveryAdapter interface {
	PostingDiscoverer
	PostingFetcher
}
type FetchError struct {
	Category   string
	Retryable  bool
	HTTPStatus int
}

func (e *FetchError) Error() string   { return "source " + e.Category }
func (e *FetchError) Transient() bool { return e.Retryable }
func (e *FetchError) RetryAfter() time.Duration {
	if e.HTTPStatus == 429 {
		return time.Minute
	}
	return 0
}
func AsFetchError(err error, target **FetchError) bool   { return errors.As(err, target) }
func fail(category string, retry bool, status int) error { return &FetchError{category, retry, status} }

// PublicPlatform has no persistence handle. It emits only page facts and refs.
type PublicPlatform struct {
	Client       *http.Client
	Allow        func(context.Context, string, int) (bool, error)
	CacheRead    func(context.Context, string, string) (HTTPEntry, error)
	CacheWrite   func(context.Context, string, string, HTTPEntry) error
	bilibiliCSRF string // operation-local anonymous token, never persisted
	sangforToken string // operation-local anonymous token, never persisted
}

type HTTPEntry struct {
	ETag, LastModified string
	Body               []byte
}

var publicPlatformClient = PublicClient()

func (PublicPlatform) Version() string { return "public-platforms-v20" }

var tenantPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

func PlatformURL(s d.Source) (string, error) {
	if !tenantPattern.MatchString(s.Tenant) {
		return "", fail("UNSUPPORTED", false, 0)
	}
	switch s.Adapter {
	case "lever":
		return "https://api.lever.co/v0/postings/" + s.Tenant, nil
	case "greenhouse":
		return "https://boards-api.greenhouse.io/v1/boards/" + s.Tenant + "/jobs", nil
	case "smartrecruiters":
		return "https://api.smartrecruiters.com/v1/companies/" + s.Tenant + "/postings", nil
	case "kedacom", "games37", "sangfor", "yonyou", "ths", "cmbnt", "netease_game", "leihuo", "ctyun", "ctcloud", "mihoyo", "pingan_tech", "pingan_oneconnect", "pingan_wallet", "cmcloud", "cmiot", "cmhome", "gbits", "tcl_digital", "tcl_honghu", "cec_software", "cmb_tech", "citic_tech", "boc_software", "boc_operations", "bankcomm_tech", "cms_securities", "htsc_securities":
		if cfg := sectorScopes[s.Adapter]; s.Tenant == cfg.Tenant {
			return cfg.Origin + "/api", nil
		}
	case "h3c", "yusys", "cksic", "whxmc", "neusoft", "mthreads", "nexchip", "lenovo", "midea", "byd", "hikvision", "qihoo360", "sany", "inovance", "vivo", "honor", "sgm", "ctrip", "tencent", "sap", "hundsun", "yuewen", "csc_securities", "guosen_securities", "galaxy_securities", "cicc_securities":
		if s.Tenant == moreTenant(s.Adapter) {
			if cfg, ok := beisenCompanies[s.Adapter]; ok {
				return cfg.Origin + "/api", nil
			}
			return map[string]string{"lenovo": lenovoOrigin + "/gateway", "midea": mideaOrigin + "/backend", "byd": bydAPI, "hikvision": hikvisionOrigin + "/api", "honor": honorOrigin + "/wecruit", "ctrip": ctripOrigin + "/api/hrrecruit", "tencent": tencentOrigin + "/api/v1", "sap": sapOrigin + "/services/jobs"}[s.Adapter], nil
		}
	case "xiaohongshu":
		return "https://job.xiaohongshu.com/websiterecruit/position", nil
	case "baidu":
		if s.Tenant == "GRADUATE" {
			return "https://talent.baidu.com/httservice", nil
		}
	case "meituan":
		if s.Tenant == "graduate" {
			return "https://zhaopin.meituan.com/api/official/job", nil
		}
	case "jd":
		if s.Tenant == "present" {
			return "https://campus.jd.com/api/wx/position", nil
		}
	case "netease":
		if s.Tenant == "103" {
			return "https://campus.163.com/api/campuspc/position", nil
		}
	case "alibaba":
		if s.Tenant == "100000760001" {
			return "https://campus-talent.alibaba.com/position", nil
		}
	case "bilibili":
		if s.Tenant == "freshmen" {
			return "https://jobs.bilibili.com/api/campus/position", nil
		}
	case "siemens":
		if s.Tenant == siemensScope {
			return siemensOrigin + "/siemens/position", nil
		}
	case "haier":
		if s.Tenant == haierProjectCode {
			return haierOrigin + "/client/campusmobile", nil
		}
	case "oppo":
		if s.Tenant == oppoProjectCode {
			return oppoOrigin + "/openapi/position", nil
		}
	case "kuaishou":
		if s.Tenant == kuaishouProjectCode {
			return kuaishouBase + "/api/v1/open/positions", nil
		}
	}
	return "", fail("UNSUPPORTED", false, 0)
}
func (a PublicPlatform) get(ctx context.Context, s d.Source, raw string, dst any) error {
	return a.request(ctx, s, http.MethodGet, raw, nil, dst)
}
func (a PublicPlatform) post(ctx context.Context, s d.Source, raw string, body any, dst any) error {
	return a.request(ctx, s, http.MethodPost, raw, body, dst)
}
func (a PublicPlatform) request(ctx context.Context, s d.Source, method, raw string, body any, dst any) error {
	err := a.requestOnce(ctx, s, method, raw, body, dst)
	var fetch *FetchError
	// All platform requests are public, read-only queries, including XHS POSTs.
	// Retry once for transport errors or 5xx, but leave rate limits and access
	// restrictions to the existing scheduler policy.
	if !errors.As(err, &fetch) || (fetch.Category != "TIMEOUT_OR_NETWORK" && !(fetch.Category == "HTTP_TRANSIENT" && fetch.HTTPStatus >= 500)) {
		return err
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return err
	case <-timer.C:
		return a.requestOnce(ctx, s, method, raw, body, dst)
	}
}

func (a PublicPlatform) allowRequest(ctx context.Context, s d.Source) error {
	if a.Allow != nil {
		limit := s.RateLimit
		if limit < 1 {
			limit = 30
		}
		key := s.ID
		if d.IsCampusSource(s.Adapter) {
			key = s.Adapter + ":public-site"
			if _, ok := hotjobCampusProjects[s.Adapter]; ok {
				if hotjobCampusProjects[s.Adapter].Origin == hotjobOrigin {
					key = "wecruit:public-site"
				}
			}
			if _, ok := tclUnits[s.Adapter]; ok {
				key = "tcl:public-site"
			}
			if s.Adapter == "ctyun" || s.Adapter == "ctcloud" {
				key = "chinatelecom:public-site"
			}
			if _, ok := pinganUnits[s.Adapter]; ok {
				key = "pingan:public-site"
			}
			if _, ok := bocUnits[s.Adapter]; ok {
				key = "boc:public-site"
			}
			if _, ok := mobileUnits[s.Adapter]; ok {
				key = "chinamobile:public-site"
			}
		}
		ok, err := a.Allow(ctx, key, limit)
		if err != nil {
			return err
		}
		if !ok {
			return fail("RATE_LIMIT", true, 429)
		}
	}
	return nil
}

func (a PublicPlatform) requestOnce(ctx context.Context, s d.Source, method, raw string, body any, dst any) error {
	if err := a.allowRequest(ctx, s); err != nil {
		return err
	}
	client := a.Client
	if client == nil {
		client = publicPlatformClient
	}
	var payload io.Reader
	contentType := "application/json"
	if body != nil {
		if form, ok := body.(url.Values); ok {
			payload = strings.NewReader(form.Encode())
			contentType = "application/x-www-form-urlencoded"
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				return fail("SCHEMA_INVALID", false, 0)
			}
			payload = bytes.NewReader(b)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, payload)
	if err != nil {
		return fail("UNSUPPORTED", false, 0)
	}
	req.Header.Set("Accept", "application/json")
	if _, ok := dst.(*htmlDocument); ok {
		req.Header.Set("Accept", "text/html")
	}
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	// These public queries use the same non-personal origin as the official UI.
	if s.Adapter == "baidu" {
		req.Header.Set("Referer", BaiduCampusURL)
	}
	if s.Adapter == "meituan" {
		req.Header.Set("Referer", MeituanCampusURL)
	}
	if s.Adapter == "siemens" || s.Adapter == "haier" {
		origin, referer := siemensOrigin, SiemensCampusURL
		if s.Adapter == "haier" {
			origin, referer = haierOrigin, HaierCampusURL
		}
		u, _ := url.Parse(origin)
		if req.URL.Scheme != "https" || req.URL.Host != u.Host {
			return fail("UNSUPPORTED", false, 0)
		}
		req.Header.Set("Referer", referer)
		if body != nil {
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
		}
	}
	if s.Adapter == "oppo" {
		if req.URL.Scheme != "https" || req.URL.Host != "careers.oppo.com" {
			return fail("UNSUPPORTED", false, 0)
		}
		req.Header.Set("Tenant-Id", "1000")
		req.Header.Set("Referer", OPPOCampusURL)
	}
	if s.Adapter == "kuaishou" {
		if req.URL.Scheme != "https" || req.URL.Host != "campus.kuaishou.cn" {
			return fail("UNSUPPORTED", false, 0)
		}
		req.Header.Set("Referer", KuaishouCampusURL)
	}
	if s.Adapter == "bilibili" {
		if req.URL.Scheme != "https" || req.URL.Host != "jobs.bilibili.com" {
			return fail("UNSUPPORTED", false, 0)
		}
		// Published client constants describe the anonymous visitor type, not
		// candidate credentials. The CSRF token comes from the public endpoint.
		req.Header.Set("X-AppKey", "ops.ehr-api.auth")
		req.Header.Set("X-UserType", "2")
		req.Header.Set("Referer", BilibiliCampusURL)
		if a.bilibiliCSRF != "" {
			req.Header.Set("X-CSRF", a.bilibiliCSRF)
		}
	}
	if moreTenant(s.Adapter) != "" {
		base, err := PlatformURL(s)
		if err != nil {
			return err
		}
		origin, _ := url.Parse(base)
		if req.URL.Scheme != "https" || req.URL.Host != origin.Host {
			return fail("UNSUPPORTED", false, 0)
		}
		req.Header.Set("Referer", expandedURL(s.Adapter))
		if s.Adapter == "sangfor" && a.sangforToken != "" && strings.HasPrefix(req.URL.Path, "/api/api/Jobs") {
			req.Header.Set("Authorization", "Bearer "+a.sangforToken)
		}
		if _, ok := tclUnits[s.Adapter]; ok {
			req.Header.Set("Origin", tclOrigin)
			req.Header.Set("X-Requested-With", "XMLHttpRequest")
		}
	}
	req.Header.Set("User-Agent", "CampusTrace/0.2 (public recruiting source monitoring)")
	key := d.Hash(a.Version() + ":" + raw)
	var cached HTTPEntry
	if method == http.MethodGet && a.CacheRead != nil {
		if entry, e := a.CacheRead(ctx, s.ID, key); e == nil && len(entry.Body) > 0 && len(entry.Body) <= 1<<20 && validPlatformBody(entry.Body, dst) {
			cached = entry
			if entry.ETag != "" {
				req.Header.Set("If-None-Match", entry.ETag)
			}
			if entry.LastModified != "" {
				req.Header.Set("If-Modified-Since", entry.LastModified)
			}
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, errCampusOffOriginRedirect) {
			return fail("BLOCKED", false, http.StatusFound)
		}
		return fail("TIMEOUT_OR_NETWORK", true, 0)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		if len(cached.Body) == 0 || (cached.ETag == "" && cached.LastModified == "") {
			return fail("SCHEMA_INVALID", false, 304)
		}
		if err = decodePlatformBody(cached.Body, dst); err != nil {
			return fail("SCHEMA_INVALID", false, 304)
		}
		if a.CacheWrite != nil {
			_ = a.CacheWrite(ctx, s.ID, key, cached)
		}
		return nil
	}
	switch {
	case resp.StatusCode == 429 || resp.StatusCode >= 500:
		return fail("HTTP_TRANSIENT", true, resp.StatusCode)
	case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 412:
		return fail("BLOCKED", false, resp.StatusCode)
	case resp.StatusCode != 200:
		return fail("HTTP_ERROR", false, resp.StatusCode)
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	_, isHTML := dst.(*htmlDocument)
	validMedia := media == "application/json" || strings.HasSuffix(media, "+json") || mobilePlainJSON(s.Adapter, method, req.URL.Path, media)
	if isHTML {
		validMedia = media == "text/html" || media == "application/xhtml+xml"
	}
	if err != nil || !validMedia {
		return fail("SCHEMA_INVALID", false, 200)
	}
	// Go's public transport transparently decompresses gzip before this bound.
	b, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return fail("HTTP_TRANSIENT", true, 200)
	}
	if len(b) > 1<<20 {
		return fail("RESPONSE_TOO_LARGE", false, 200)
	}
	if err = decodePlatformBody(b, dst); err != nil {
		return fail("SCHEMA_INVALID", false, 200)
	}
	if method == http.MethodGet && a.CacheWrite != nil {
		entry := HTTPEntry{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), Body: b}
		// Replace even a formerly validated response whose validator was removed.
		_ = a.CacheWrite(ctx, s.ID, key, entry)
	}
	return nil
}

type leverPosting struct {
	ID              string `json:"id"`
	Text            string `json:"text"`
	HostedURL       string `json:"hostedUrl"`
	ApplyURL        string `json:"applyUrl"`
	Description     string `json:"descriptionPlain"`
	DescriptionHTML string `json:"description"`
	Additional      string `json:"additionalPlain"`
	Lists           []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"lists"`
	Categories struct {
		Location   string `json:"location"`
		Commitment string `json:"commitment"`
	} `json:"categories"`
}
type greenhousePosting struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"absolute_url"`
	Content  string `json:"content"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
}
type smartPosting struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ApplyURL string `json:"applyUrl"`
	Location struct {
		City    string `json:"city"`
		Country string `json:"country"`
	} `json:"location"`
	JobAd struct {
		Sections map[string]struct {
			Text string `json:"text"`
		} `json:"sections"`
	} `json:"jobAd"`
	TypeOfEmployment struct {
		Label string `json:"label"`
	} `json:"typeOfEmployment"`
}

func jobType(raw string) string {
	switch strings.ToLower(raw) {
	case "full-time", "full time":
		return "FULL_TIME"
	case "intern", "internship":
		return "INTERNSHIP"
	}
	return "UNKNOWN"
}
func (a PublicPlatform) Discover(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	base, err := PlatformURL(s)
	if err != nil {
		return nil, err
	}
	refs := []PostingRef{}
	switch s.Adapter {
	case "lever":
		for offset := 0; offset <= MaxPostings; offset += 100 {
			var rows []leverPosting
			if err = a.get(ctx, s, fmt.Sprintf("%s?mode=json&limit=100&skip=%d", base, offset), &rows); err != nil {
				return nil, err
			}
			if rows == nil {
				return nil, fail("SCHEMA_INVALID", false, 200)
			}
			for _, v := range rows {
				refs = append(refs, PostingRef{v.ID, v.HostedURL, v.Text, s.Name, jobType(v.Categories.Commitment), []string{v.Categories.Location}})
			}
			if len(rows) < 100 {
				break
			}
			if len(refs) >= MaxPostings {
				return nil, fail("CAPACITY", false, 200)
			}
		}
	case "greenhouse":
		var data struct {
			Jobs []greenhousePosting `json:"jobs"`
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if err = a.get(ctx, s, base, &data); err != nil {
			return nil, err
		}
		if data.Jobs == nil || data.Meta.Total > len(data.Jobs) {
			return nil, fail("SCHEMA_INVALID", false, 200)
		}
		for _, v := range data.Jobs {
			refs = append(refs, PostingRef{strconv.FormatInt(v.ID, 10), v.URL, v.Title, s.Name, "UNKNOWN", []string{v.Location.Name}})
		}
	case "smartrecruiters":
		for offset := 0; offset <= MaxPostings; offset += 100 {
			var data struct {
				Total   int            `json:"totalFound"`
				Content []smartPosting `json:"content"`
			}
			if err = a.get(ctx, s, fmt.Sprintf("%s?limit=100&offset=%d", base, offset), &data); err != nil {
				return nil, err
			}
			if data.Content == nil || data.Total > MaxPostings {
				return nil, fail("CAPACITY_OR_SCHEMA", false, 200)
			}
			for _, v := range data.Content {
				refs = append(refs, PostingRef{v.ID, "https://jobs.smartrecruiters.com/" + s.Tenant + "/" + v.ID, v.Name, s.Name, jobType(v.TypeOfEmployment.Label), []string{v.Location.City, v.Location.Country}})
			}
			if len(refs) >= data.Total {
				break
			}
			if len(data.Content) == 0 {
				return nil, fail("SCHEMA_INVALID", false, 200)
			}
		}
	case "kedacom", "games37", "sangfor", "yonyou", "h3c", "yusys", "cksic", "whxmc", "neusoft", "mthreads", "nexchip", "lenovo", "midea", "byd", "hikvision", "qihoo360", "sany", "inovance", "vivo", "honor", "sgm", "ctrip", "ths", "cmbnt", "netease_game", "leihuo", "ctyun", "ctcloud", "mihoyo", "pingan_tech", "pingan_oneconnect", "pingan_wallet", "cmcloud", "cmiot", "cmhome", "gbits", "hundsun", "yuewen", "tcl_digital", "tcl_honghu", "cec_software", "tencent", "sap", "cmb_tech", "citic_tech", "boc_software", "boc_operations", "bankcomm_tech", "cms_securities", "htsc_securities", "csc_securities", "guosen_securities", "galaxy_securities", "cicc_securities":
		refs, err = a.discoverMore(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "xiaohongshu":
		refs, err = a.discoverXHS(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "baidu", "meituan":
		refs, err = a.discoverGraduate(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "jd", "netease":
		refs, err = a.discoverPortal(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "alibaba":
		refs, err = a.discoverAlibaba(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "bilibili":
		refs, err = a.discoverBilibili(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "siemens":
		refs, err = a.discoverSiemens(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "haier":
		refs, err = a.discoverHaier(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "oppo":
		refs, err = a.discoverOPPO(ctx, s, w)
		if err != nil {
			return nil, err
		}
	case "kuaishou":
		refs, err = a.discoverKuaishou(ctx, s, w)
		if err != nil {
			return nil, err
		}
	}
	if len(refs) > MaxPostings {
		return nil, fail("CAPACITY", false, 200)
	}
	out := []PostingRef{}
	seen := map[string]bool{}
	for _, r := range refs {
		if err := validateRef(r); err != nil {
			return nil, err
		}
		if seen[r.ExternalID] {
			return nil, fail("SCHEMA_DUPLICATE_ID", false, 200)
		}
		seen[r.ExternalID] = true
		if w.Keyword == "" || strings.Contains(strings.ToLower(r.Title+" "+strings.Join(r.Locations, " ")), strings.ToLower(w.Keyword)) {
			out = append(out, r)
		}
	}
	return out, nil
}
func validateRef(r PostingRef) error {
	u, err := url.Parse(r.URL)
	if r.ExternalID == "" || len(r.ExternalID) > 200 || r.ExternalID == "0" || r.Title == "" || len(r.Title) > 300 || len(r.Locations) > d.MaxJobLocations || err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) FetchPosting(ctx context.Context, s d.Source, r PostingRef) (Result, error) {
	base, err := PlatformURL(s)
	if err != nil {
		return Result{}, err
	}
	if err = validateRef(r); err != nil {
		return Result{}, err
	}
	raw := base + "/" + url.PathEscape(r.ExternalID)
	text := ""
	switch s.Adapter {
	case "lever":
		var v leverPosting
		if err = a.get(ctx, s, raw, &v); err == nil {
			if v.ID != r.ExternalID || v.Text == "" {
				err = fail("SCHEMA_INVALID", false, 200)
				break
			}
			text = v.Text + "\n" + v.Description
			if v.Description == "" {
				text += "\n" + plainHTML(v.DescriptionHTML)
			}
			for _, part := range v.Lists {
				text += "\n" + part.Text + "\n" + plainHTML(part.Content)
			}
			text += "\n" + v.Additional
			if v.ApplyURL != "" {
				text += "\nApplication URL: " + v.ApplyURL
			}
		}
	case "greenhouse":
		var v greenhousePosting
		if err = a.get(ctx, s, raw, &v); err == nil {
			if strconv.FormatInt(v.ID, 10) != r.ExternalID || v.Content == "" {
				err = fail("SCHEMA_INVALID", false, 200)
				break
			}
			text = v.Title + "\n" + plainHTML(v.Content)
		}
	case "smartrecruiters":
		var v smartPosting
		if err = a.get(ctx, s, raw, &v); err == nil {
			if v.ID != r.ExternalID || len(v.JobAd.Sections) == 0 {
				err = fail("SCHEMA_INVALID", false, 200)
				break
			}
			text = v.Name
			for _, key := range []string{"companyDescription", "jobDescription", "qualifications", "additionalInformation"} {
				text += "\n" + plainHTML(v.JobAd.Sections[key].Text)
			}
			if v.ApplyURL != "" {
				text += "\nApplication URL: " + v.ApplyURL
			}
		}
	case "kedacom", "games37", "sangfor", "yonyou", "h3c", "yusys", "cksic", "whxmc", "neusoft", "mthreads", "nexchip", "lenovo", "midea", "byd", "hikvision", "qihoo360", "sany", "inovance", "vivo", "honor", "sgm", "ctrip", "ths", "cmbnt", "netease_game", "leihuo", "ctyun", "ctcloud", "mihoyo", "pingan_tech", "pingan_oneconnect", "pingan_wallet", "cmcloud", "cmiot", "cmhome", "gbits", "hundsun", "yuewen", "tcl_digital", "tcl_honghu", "cec_software", "tencent", "sap", "cmb_tech", "citic_tech", "boc_software", "boc_operations", "bankcomm_tech", "cms_securities", "htsc_securities", "csc_securities", "guosen_securities", "galaxy_securities", "cicc_securities":
		text, err = a.fetchMore(ctx, s, r)
	case "xiaohongshu":
		text, err = a.fetchXHS(ctx, s, r)
	case "baidu", "meituan":
		text, err = a.fetchGraduate(ctx, s, r)
	case "jd", "netease":
		text, err = a.fetchPortal(ctx, s, r)
	case "alibaba":
		text, err = a.fetchAlibaba(ctx, s, r)
	case "bilibili":
		text, err = a.fetchBilibili(ctx, s, r)
	case "siemens":
		text, err = a.fetchSiemens(ctx, s, r)
	case "haier":
		text, err = a.fetchHaier(ctx, s, r)
	case "oppo":
		text, err = a.fetchOPPO(ctx, s, r)
	case "kuaishou":
		text, err = a.fetchKuaishou(ctx, s, r)
	}
	if err != nil {
		res := Result{Status: "HTTP_ERROR"}
		var e *FetchError
		if errors.As(err, &e) {
			if e.Category == "RATE_LIMIT" {
				return Result{}, err
			}
			res.HTTPStatus = e.HTTPStatus
			switch e.Category {
			case "BLOCKED":
				res.Status = "BLOCKED"
			case "TIMEOUT_OR_NETWORK":
				res.Status = "TIMEOUT"
			case "SCHEMA_INVALID", "RESPONSE_TOO_LARGE":
				res.Status = "PARSE_ERROR"
			}
		}
		return res, err
	}
	if strings.TrimSpace(text) == "" || len(text) > 60000 {
		return Result{Status: "PARSE_ERROR"}, fail("SCHEMA_INVALID", false, 200)
	}
	return Result{Text: text, Status: "SUCCESS", HTTPStatus: 200}, nil
}
func plainHTML(raw string) string {
	root, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return ""
	}
	parts := []string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return
		}
		if n.Type == html.TextNode && strings.TrimSpace(n.Data) != "" {
			parts = append(parts, strings.TrimSpace(n.Data))
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return strings.Join(parts, "\n")
}
