package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func bilibiliFixture(t *testing.T, scenario string) *httptest.Server {
	t.Helper()
	var issued atomic.Int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-AppKey") != "ops.ehr-api.auth" || r.Header.Get("X-UserType") != "2" || r.Header.Get("Referer") != BilibiliCampusURL {
			t.Error("must reproduce public anonymous client headers")
		}
		if r.URL.Path == "/api/auth/v1/csrf/token" {
			if r.Method != "GET" || r.Header.Get("X-CSRF") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("If-None-Match") != "" {
				t.Error("bootstrap must use a fresh anonymous session without cached token")
			}
			if scenario == "http_blocked" {
				w.WriteHeader(412)
				return
			}
			if scenario == "redirect" {
				w.Header().Set("Location", "https://example.org/")
				w.WriteHeader(302)
				return
			}
			if scenario == "blocked" {
				json.NewEncoder(w).Encode(map[string]any{"code": -101, "data": nil})
				return
			}
			token := fmt.Sprintf("public-token-%d", issued.Add(1))
			if scenario == "empty_token" {
				token = ""
			}
			if scenario == "invalid_token" {
				token = "bad\r\ntoken"
			}
			if scenario != "invalid_token" {
				http.SetCookie(w, &http.Cookie{Name: "ANONYMOUS", Value: token, Path: "/"})
			}
			w.Header().Set("ETag", "token-must-not-be-cached")
			v := map[string]any{"code": 0, "data": token}
			if scenario == "missing_code" {
				delete(v, "code")
			}
			json.NewEncoder(w).Encode(v)
			return
		}
		cookie, err := r.Cookie("ANONYMOUS")
		if err != nil || r.Header.Get("X-CSRF") == "" || cookie.Value != r.Header.Get("X-CSRF") {
			t.Error("session/token crossed operations")
		}
		if scenario == "expired" {
			json.NewEncoder(w).Encode(map[string]any{"code": -111, "data": nil})
			return
		}
		row := func(n int) map[string]any {
			return map[string]any{"id": n, "campusProjectId": 55, "positionName": "研发工程师 " + strconv.Itoa(n), "positionTypeName": "全职", "positionType": "3", "recruitType": 1, "workLocation": "上海", "positionDescription": "<b>工作职责:</b>\n开发 Go 服务\n工作要求:\n熟悉 MySQL", "webApplyStartTime": "2026-08-03 00:00:00", "webApplyEndTime": "2026-12-31 00:00:00", "graduationStartTime": "2026-09-01 00:00:00", "graduationEndTime": "2027-08-01 00:00:00"}
		}
		if strings.HasPrefix(r.URL.Path, "/api/campus/position/detail/") {
			if r.Method != "GET" {
				t.Error("detail must be public GET")
			}
			id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/campus/position/detail/"))
			v := row(id)
			switch scenario {
			case "wrong_id":
				v["id"] = id + 1
			case "intern_detail":
				v["positionType"] = "0"
			case "wrong_recruit":
				v["recruitType"] = 0
			case "empty_text":
				v["positionDescription"] = ""
			case "fallback_text":
				v["positionDescriptions"] = v["positionDescription"]
				delete(v, "positionDescription")
			}
			w.Header().Set("ETag", "public-posting-v1")
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": v})
			return
		}
		if r.URL.Path != "/api/campus/position/positionList" || r.Method != "POST" {
			t.Error("unexpected public query")
		}
		var body struct {
			Page          int      `json:"pageNum"`
			Size          int      `json:"pageSize"`
			Recruit       int      `json:"recruitType"`
			WorkTypes     []string `json:"workTypeList"`
			PositionTypes []string `json:"positionTypeList"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Size != 10 || body.Recruit != 1 || strings.Join(body.WorkTypes, ",") != "3" || strings.Join(body.PositionTypes, ",") != "3" {
			t.Error("graduate scope must exclude internships")
		}
		total := 19
		rows := []any{}
		for n := (body.Page-1)*10 + 1; n <= min(body.Page*10, total); n++ {
			v := row(n)
			delete(v, "positionType")
			rows = append(rows, v)
		}
		switch scenario {
		case "partial":
			rows = rows[:len(rows)-1]
		case "drift":
			if body.Page == 2 {
				total = 20
			}
		case "capacity":
			total = 501
		case "duplicate":
			if body.Page == 2 {
				rows[0] = row(1)
			}
		case "intern":
			rows[0].(map[string]any)["positionTypeName"] = "实习"
		case "hidden":
			rows[0].(map[string]any)["positionName"] = "内部岗位#012830"
		}
		// Mirror the API bug: a short final page changes both size and pages.
		v := map[string]any{"total": total, "list": rows, "size": len(rows), "pages": (total + len(rows) - 1) / len(rows)}
		if scenario == "missing_total" {
			delete(v, "total")
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": v})
	}))
}
func TestBilibiliAnonymousScopeFullPaginationAndOriginal(t *testing.T) {
	srv := bilibiliFixture(t, "")
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	origin, _ := url.Parse(bilibiliOrigin)
	jar.SetCookies(origin, []*http.Cookie{{Name: "PRIVATE", Value: "never-copy-browser-credentials"}})
	cached, keys := []string{}, []string{}
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}, Jar: jar}, Allow: func(_ context.Context, key string, limit int) (bool, error) {
		keys = append(keys, key)
		if limit != 30 {
			t.Errorf("limit %d", limit)
		}
		return true, nil
	}, CacheRead: func(_ context.Context, id, key string) (HTTPEntry, error) {
		cached = append(cached, "read")
		return HTTPEntry{}, fmt.Errorf("empty cache")
	}, CacheWrite: func(_ context.Context, id, key string, v HTTPEntry) error {
		cached = append(cached, "write")
		if strings.Contains(string(v.Body), "public-token") {
			t.Error("token persisted")
		}
		return nil
	}}
	ctx := context.Background()
	v, err := a.PreviewCampus(ctx, BilibiliCampusURL)
	if err != nil || v.Total != 19 || v.Adapter != "bilibili" || v.ProjectCode != "freshmen" || v.MinimumInterval != 1800 || v.SupportsDirection {
		t.Fatalf("preview %+v %v", v, err)
	}
	s := d.Source{ID: "fixture", Adapter: "bilibili", Tenant: "freshmen"}
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	if err != nil || len(refs) != 19 {
		t.Fatalf("complete %d %v", len(refs), err)
	}
	filtered, err := a.Discover(ctx, s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "工程师 19"}})
	if err != nil || len(filtered) != 1 {
		t.Fatalf("filtered %d %v", len(filtered), err)
	}
	text, err := a.FetchPosting(ctx, s, refs[18])
	if err != nil || !strings.Contains(text.Text, "开发 Go 服务") || !strings.Contains(text.Text, "网申截止日期：2026-12-31 00:00:00") || !strings.Contains(text.Text, "2027-08-01") {
		t.Fatalf("original %+v %v", text, err)
	}
	if len(cached) != 2 || a.bilibiliCSRF != "" || a.Client.Jar != jar {
		t.Fatalf("session/cache escaped operation: %v", cached)
	}
	for _, key := range keys {
		if key != "bilibili:public-site" {
			t.Errorf("unshared pacing key %s", key)
		}
	}
	if _, err = a.Discover(ctx, s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil {
		t.Fatal("unsupported direction accepted")
	}
}
func TestBilibiliScopeErrorsAndAccessLimits(t *testing.T) {
	for _, scenario := range []string{"partial", "drift", "capacity", "duplicate", "intern", "hidden", "missing_total", "blocked", "expired", "http_blocked", "redirect", "empty_token", "invalid_token", "missing_code", "wrong_id", "intern_detail", "wrong_recruit", "empty_text", "fallback_text"} {
		t.Run(scenario, func(t *testing.T) {
			srv := bilibiliFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{ID: "fixture", Adapter: "bilibili", Tenant: "freshmen"}
			var err error
			switch scenario {
			case "wrong_id", "intern_detail", "wrong_recruit", "empty_text", "fallback_text":
				_, err = a.FetchPosting(context.Background(), s, PostingRef{ExternalID: "19", Title: "研发工程师 19", URL: bilibiliOrigin + "/campus/positions/19"})
			default:
				_, err = a.Discover(context.Background(), s, d.WatchTarget{})
			}
			if scenario == "fallback_text" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe response accepted")
			}
			if scenario == "http_blocked" || scenario == "blocked" || scenario == "expired" {
				var v *FetchError
				if !AsFetchError(err, &v) || v.Category != "BLOCKED" || v.Retryable {
					t.Fatalf("access limitation misclassified: %v", err)
				}
			}
		})
	}
}
func TestBilibiliStrictURLAndRefBeforeNetwork(t *testing.T) {
	for _, raw := range []string{BilibiliCampusURL, bilibiliOrigin + "/campus/positions"} {
		if RecognizeCampusURL(raw) != nil {
			t.Errorf("canonical rejected %s", raw)
		}
	}
	for _, raw := range []string{bilibiliOrigin + "/campus/positions?type=0", BilibiliCampusURL + "&type=0", BilibiliCampusURL + "&channel=referral", BilibiliCampusURL + "#login", "https://jobs.bilibili.com.evil.test/campus/positions", "https://user:password@jobs.bilibili.com/campus/positions", "https://jobs.bilibili.com:443/campus/positions"} {
		if RecognizeCampusURL(raw) == nil {
			t.Errorf("untrusted scope accepted %s", raw)
		}
	}
	calls := 0
	a := PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected network") })}, bilibiliCSRF: "private-test-token"}
	s := d.Source{Adapter: "bilibili", Tenant: "freshmen"}
	if _, err := a.FetchPosting(context.Background(), s, PostingRef{ExternalID: "19", Title: "研发工程师 19", URL: bilibiliOrigin + "/campus/positions/20"}); err == nil {
		t.Fatal("forged ref accepted")
	}
	s.Tenant = "intern"
	if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil {
		t.Fatal("wrong scope accepted")
	}
	var dst any
	if err := a.get(context.Background(), s, "https://example.org/", &dst); err == nil {
		t.Fatal("token sent to another origin")
	}
	if calls != 0 {
		t.Fatal("invalid reference/scope reached network")
	}
}
func TestBilibiliLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("opt-in public network verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	v, err := (PublicPlatform{}).PreviewCampus(ctx, BilibiliCampusURL)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	a := PublicPlatform{}
	s := d.Source{ID: "live-bilibili", Adapter: "bilibili", Tenant: v.ProjectCode}
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	cancel()
	if err != nil || len(refs) == 0 || len(refs) != v.Total {
		t.Fatalf("complete %d of %d: %v", len(refs), v.Total, err)
	}
	for _, r := range refs {
		if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []PostingRef{refs[0], refs[len(refs)-1]} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		v, err := a.FetchPosting(ctx, s, r)
		cancel()
		if err != nil || !strings.Contains(v.Text, "岗位描述") {
			t.Fatalf("detail %s %v", r.ExternalID, err)
		}
	}
	t.Logf("company=哔哩哔哩 complete_count=%d first=%s last=%s", len(refs), refs[0].ExternalID, refs[len(refs)-1].ExternalID)
}
