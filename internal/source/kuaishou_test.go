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
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func kuaishouFixture(t *testing.T, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != KuaishouCampusURL {
			t.Error("public recruiting query must use no candidate credentials")
		}
		if scenario == "http_blocked" {
			w.WriteHeader(403)
			return
		}
		if scenario == "redirect" {
			w.Header().Set("Location", "https://example.org/")
			w.WriteHeader(302)
			return
		}
		write := func(result any) {
			v := map[string]any{"code": 0, "result": result}
			if scenario == "blocked" {
				v["code"] = 401
			}
			if scenario == "missing_code" {
				delete(v, "code")
			}
			json.NewEncoder(w).Encode(v)
		}
		if strings.HasSuffix(r.URL.Path, "/sub-project/findByCode") {
			if r.Method != "GET" || r.URL.Query().Get("code") != kuaishouProjectCode {
				t.Error("unscoped project query")
			}
			v := map[string]any{"code": kuaishouProjectCode, "name": "2027应届生", "year": "2027", "projectType": "fulltime", "active": true, "graduateStartTime": int64(1793462400000), "graduateEndTime": int64(1824912000000)}
			switch scenario {
			case "wrong_project":
				v["code"] = "20271772783534"
			case "wrong_year":
				v["year"] = "2026"
			case "wrong_name":
				v["name"] = "2027留用实习"
			case "inactive":
				v["active"] = false
			case "missing_active":
				delete(v, "active")
			case "intern_project":
				v["projectType"] = "intern"
			case "bad_dates":
				v["graduateEndTime"] = 1
			case "job_graduation":
				if r.URL.Query().Get("positionId") != "79" {
					t.Error("detail must request its own published graduation scope")
				}
			}
			write(v)
			return
		}
		row := func(n int) map[string]any {
			return map[string]any{"id": n, "name": "Go 研发工程师 " + strconv.Itoa(n), "recruitProjectCode": "schoolr", "recruitSubProjectCode": kuaishouProjectCode, "positionNatureCode": "fulltime", "positionStatusCode": "Release", "ifShowRecruitWebsite": true, "description": "<p>开发 Go 服务</p>", "positionDemand": "熟悉 MySQL，Redis 加分", "workLocationDicts": []any{map[string]any{"name": "北京"}, map[string]any{"name": "杭州"}, map[string]any{"name": "北京"}}}
		}
		if strings.HasSuffix(r.URL.Path, "/positions/find") {
			if r.Method != "GET" {
				t.Error("detail must be a read-only GET")
			}
			id, _ := strconv.Atoi(r.URL.Query().Get("id"))
			v := row(id)
			switch scenario {
			case "wrong_id":
				v["id"] = id + 1
			case "detail_intern":
				v["positionNatureCode"] = "intern"
			case "detail_closed":
				v["positionStatusCode"] = "Close"
			case "empty_duties":
				v["description"] = "<p> </p>"
			case "empty_requirements":
				v["positionDemand"] = ""
			}
			write(v)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/positions/simple") || r.Method != "POST" {
			t.Error("unexpected endpoint")
		}
		var input struct {
			Page     int      `json:"pageNum"`
			Size     int      `json:"pageSize"`
			Projects []string `json:"recruitSubProjectCodes"`
			Name     string   `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Size != kuaishouPageSize || strings.Join(input.Projects, ",") != kuaishouProjectCode || input.Name != "" {
			t.Error("must read the complete graduate cohort before filtering")
		}
		total := 79
		rows := []any{}
		for n := (input.Page-1)*input.Size + 1; n <= min(input.Page*input.Size, total); n++ {
			rows = append(rows, row(n))
		}
		switch scenario {
		case "partial":
			rows = rows[:len(rows)-1]
		case "drift":
			if input.Page == 2 {
				total = 80
			}
		case "duplicate":
			if input.Page == 2 {
				rows[0] = row(1)
			}
		case "capacity":
			total = 501
		case "empty":
			total, rows = 0, []any{}
		case "list_intern":
			rows[0].(map[string]any)["positionNatureCode"] = "intern"
		case "wrong_cohort":
			rows[0].(map[string]any)["recruitSubProjectCode"] = "20271772783534"
		case "social":
			rows[0].(map[string]any)["recruitProjectCode"] = "socialr"
		case "hidden":
			rows[0].(map[string]any)["ifShowRecruitWebsite"] = false
		case "missing_visibility":
			delete(rows[0].(map[string]any), "ifShowRecruitWebsite")
		}
		v := map[string]any{"total": total, "pageNum": input.Page, "pageSize": input.Size, "pages": (total + input.Size - 1) / input.Size, "list": rows}
		switch scenario {
		case "missing_total":
			delete(v, "total")
		case "wrong_page":
			v["pageNum"] = input.Page + 1
		case "wrong_size":
			v["pageSize"] = 10
		case "wrong_pages":
			v["pages"] = 99
		}
		write(v)
	}))
}

func TestKuaishouGraduateScopePaginationAndOriginal(t *testing.T) {
	srv := kuaishouFixture(t, "")
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	origin, _ := url.Parse(kuaishouOrigin)
	jar.SetCookies(origin, []*http.Cookie{{Name: "PRIVATE", Value: "must-not-copy"}})
	keys := []string{}
	a := PublicPlatform{Client: &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}, Allow: func(_ context.Context, key string, limit int) (bool, error) {
		keys = append(keys, key)
		if limit != 30 {
			t.Errorf("unexpected limit %d", limit)
		}
		return true, nil
	}}
	v, err := a.PreviewCampus(context.Background(), KuaishouCampusURL)
	if err != nil || v.Total != 79 || v.Adapter != "kuaishou" || v.ProjectCode != kuaishouProjectCode || v.MinimumInterval != 1800 || v.SupportsDirection || len(v.Samples) != 5 || len(keys) != 2 {
		t.Fatalf("preview %+v %v calls=%d", v, err, len(keys))
	}
	s := d.Source{ID: "fixture", Adapter: "kuaishou", Tenant: kuaishouProjectCode}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil || len(refs) != 79 || strings.Join(refs[0].Locations, ",") != "北京,杭州" || refs[0].Company != "快手" || refs[0].JobType != "FULL_TIME" || len(keys) != 5 {
		t.Fatalf("complete %d %v calls=%d", len(refs), err, len(keys))
	}
	filtered, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "工程师 79"}})
	if err != nil || len(filtered) != 1 || filtered[0].ExternalID != "79" {
		t.Fatalf("filter %+v %v", filtered, err)
	}
	result, err := a.FetchPosting(context.Background(), s, refs[78])
	if err != nil || !strings.Contains(result.Text, "开发 Go 服务") || !strings.Contains(result.Text, "熟悉 MySQL，Redis 加分") || !strings.Contains(result.Text, "毕业时间范围") || strings.Contains(result.Text, "<p>") || result.Status != "SUCCESS" {
		t.Fatalf("original %+v %v", result, err)
	}
	if a.Client.Jar != jar {
		t.Fatal("caller client modified")
	}
	for _, key := range keys {
		if key != "kuaishou:public-site" {
			t.Errorf("unshared pacing key %s", key)
		}
	}
}

func TestKuaishouRejectsPartialOrWrongScope(t *testing.T) {
	for _, scenario := range []string{"wrong_project", "wrong_year", "wrong_name", "inactive", "missing_active", "intern_project", "bad_dates", "partial", "drift", "duplicate", "capacity", "list_intern", "wrong_cohort", "social", "hidden", "missing_visibility", "missing_total", "wrong_page", "wrong_size", "wrong_pages", "missing_code", "blocked", "http_blocked", "redirect", "wrong_id", "detail_intern", "detail_closed", "empty_duties", "empty_requirements", "empty", "job_graduation"} {
		t.Run(scenario, func(t *testing.T) {
			srv := kuaishouFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{ID: "fixture", Adapter: "kuaishou", Tenant: kuaishouProjectCode}
			var err error
			switch scenario {
			case "wrong_id", "detail_intern", "detail_closed", "empty_duties", "empty_requirements", "job_graduation":
				_, err = a.FetchPosting(context.Background(), s, PostingRef{ExternalID: "79", Title: "Go 研发工程师 79", URL: kuaishouBase + "/#/campus/job-info/79"})
			default:
				_, err = a.Discover(context.Background(), s, d.WatchTarget{})
			}
			if scenario == "empty" || scenario == "job_graduation" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe response accepted")
			}
			if scenario == "blocked" || scenario == "http_blocked" {
				var e *FetchError
				if !AsFetchError(err, &e) || e.Category != "BLOCKED" || e.Retryable {
					t.Fatalf("wrong access-limit policy %v", err)
				}
			}
		})
	}
}

func TestKuaishouStrictURLRefAndRateDeferral(t *testing.T) {
	for _, raw := range []string{KuaishouCampusURL, kuaishouBase + "/", kuaishouBase + "/#/campus/index", kuaishouBase + "/#/campus/jobs"} {
		if RecognizeCampusURL(raw) != nil {
			t.Errorf("public graduate entry rejected %s", raw)
		}
	}
	for _, raw := range []string{kuaishouBase + "/#/campus/jobs?recruitSubProjectCodes=20271772783534", KuaishouCampusURL + "&positionLabel=kstar", KuaishouCampusURL + "&pageNum=2", kuaishouBase + "/?code=referral#/campus/jobs", "https://campus.kuaishou.cn.evil.test/recruit/campus/e/", "https://user:password@campus.kuaishou.cn/recruit/campus/e/", "https://campus.kuaishou.cn:443/recruit/campus/e/", kuaishouBase + "/#/campus/talent", kuaishouOrigin + "/"} {
		if RecognizeCampusURL(raw) == nil {
			t.Errorf("unimplemented scope accepted %s", raw)
		}
	}
	calls := 0
	a := PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected network") })}}
	s := d.Source{ID: "fixture", Adapter: "kuaishou", Tenant: kuaishouProjectCode}
	if _, err := a.FetchPosting(context.Background(), s, PostingRef{ExternalID: "79", Title: "Go 研发工程师", URL: "https://example.org/79"}); err == nil {
		t.Fatal("forged URL accepted")
	}
	if _, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil {
		t.Fatal("unimplemented direction accepted")
	}
	s.Tenant = "20271772783534"
	if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil {
		t.Fatal("intern cohort accepted")
	}
	if calls != 0 {
		t.Fatal("invalid scope reached network")
	}
	srv := kuaishouFixture(t, "")
	defer srv.Close()
	s.Tenant = kuaishouProjectCode
	allowed := 0
	a = PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}, Allow: func(context.Context, string, int) (bool, error) { allowed++; return allowed < 3, nil }}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	var e *FetchError
	if len(refs) != 0 || !AsFetchError(err, &e) || e.Category != "RATE_LIMIT" || !e.Retryable {
		t.Fatalf("partial import after pacing %+v %v", refs, err)
	}
}

func TestKuaishouLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("opt-in public website verification")
	}
	a := PublicPlatform{}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	preview, err := a.PreviewCampus(ctx, KuaishouCampusURL)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	s := d.Source{ID: "live-kuaishou", Adapter: "kuaishou", Tenant: preview.ProjectCode}
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	cancel()
	if err != nil || len(refs) == 0 || len(refs) != preview.Total {
		t.Fatalf("complete %d of %d: %v", len(refs), preview.Total, err)
	}
	for _, r := range refs {
		if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "original public posting", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []PostingRef{refs[0], refs[len(refs)-1]} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		result, err := a.FetchPosting(ctx, s, r)
		cancel()
		if err != nil || !strings.Contains(result.Text, "任职要求") || !strings.Contains(result.Text, "毕业时间范围") {
			t.Fatalf("detail %s %v", r.ExternalID, err)
		}
	}
	t.Logf("company=快手 complete_count=%d first=%s last=%s", len(refs), refs[0].ExternalID, refs[len(refs)-1].ExternalID)
}
