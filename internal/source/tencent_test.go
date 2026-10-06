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

func tencentFixture(t *testing.T, scenario *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != TencentCampusURL {
			t.Error("candidate session inherited or missing public referer")
		}
		if *scenario == "blocked" {
			w.WriteHeader(403)
			return
		}
		if *scenario == "redirect" {
			http.Redirect(w, r, "https://example.com/login", 302)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var data any
		switch r.URL.Path {
		case "/api/v1/position/getProjectMapping":
			if r.Method != "GET" || r.URL.RawQuery != "" {
				t.Error("mapping request changed")
			}
			project := tencentProject{MappingID: 1, Type: 1, ProjectID: "1", Year: "2027", Status: 1, Name: "2027校园招聘", Range: "2026年1月1日-2027年12月31日"}
			if *scenario == "next_year" {
				project.Year = "2028"
			}
			if *scenario == "inactive" {
				project.Status = 0
			}
			if *scenario == "missing_range" {
				project.Range = ""
			}
			data = []any{map[string]any{"recruitType": 1, "status": 1, "subProjectList": []any{project}}, map[string]any{"recruitType": 2, "status": 1, "subProjectList": []any{map[string]any{"mappingId": 2, "recruitType": 2, "projectId": "2", "recruitYear": "2027", "status": 1}}}}
		case "/api/v1/position/searchPosition":
			if r.Method != "POST" {
				t.Error("list method changed")
			}
			var body struct {
				Mappings []int  `json:"projectMappingIdList"`
				Keyword  string `json:"keyword"`
				Country  int    `json:"workCountryType"`
				Page     int    `json:"pageIndex"`
				Size     int    `json:"pageSize"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Mappings) != 1 || body.Mappings[0] != 1 || body.Keyword != "" || body.Size != 100 || body.Page < 1 || body.Country != 1 {
				t.Error("campus request scope changed")
			}
			total := 102
			if *scenario == "capacity" {
				total = 501
			}
			if *scenario == "empty" {
				total = 0
			}
			if *scenario == "changed_total" && body.Page == 2 {
				total++
			}
			rows := []any{}
			for n := (body.Page-1)*100 + 1; n <= min(body.Page*100, total); n++ {
				id, project, src := strconv.Itoa(n), 1, "oa"
				if *scenario == "duplicate" && n == 101 {
					id = "1"
				}
				if *scenario == "intern" && n == 1 {
					project = 2
				}
				if *scenario == "third_party" && n == 1 {
					src = "external"
				}
				rows = append(rows, map[string]any{"postId": id, "projectId": project, "positionSource": src, "positionTitle": "Go 服务端 " + strconv.Itoa(n), "workCities": "深圳总部 北京 "})
			}
			if *scenario == "truncated" && body.Page == 2 {
				rows = rows[:1]
			}
			data = map[string]any{"count": total, "positionList": rows}
		case "/api/v1/jobDetails/getJobDetailsByPostId":
			if r.Method != "GET" || len(r.URL.Query()) != 1 {
				t.Error("detail request changed")
			}
			id := r.URL.Query().Get("postId")
			job := map[string]any{"postId": id, "title": "Go 服务端 " + id, "projectId": 1, "recruitType": 1, "isQingyun": 0, "workCityList": []string{"深圳总部", "北京"}, "desc": "<p>开发 Go 服务</p>", "request": "Go、Linux", "graduateBonus": "Redis 加分"}
			switch *scenario {
			case "wrong_id":
				job["postId"] = "999"
			case "wrong_title":
				job["title"] = "其他岗位"
			case "wrong_project":
				job["projectId"] = 14
			case "wrong_type":
				job["recruitType"] = 2
			case "qingyun":
				job["isQingyun"] = 1
			case "missing_type":
				delete(job, "recruitType")
			case "missing_qingyun":
				delete(job, "isQingyun")
			case "no_duties":
				job["desc"] = "<p> </p>"
			case "no_requirements":
				job["request"] = ""
			}
			data = job
		default:
			t.Errorf("unexpected public API %s", r.URL.Path)
		}
		v := map[string]any{"status": 0, "data": data}
		if *scenario == "missing_status" {
			delete(v, "status")
		}
		json.NewEncoder(w).Encode(v)
	}))
}

func TestTencentCampusScopeAndOriginals(t *testing.T) {
	scenario := ""
	srv := tencentFixture(t, &scenario)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(TencentCampusURL)
	jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
	calls := 0
	a := PublicPlatform{Client: &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}, Allow: func(_ context.Context, key string, limit int) (bool, error) {
		if key != "tencent:public-site" || limit != 30 {
			t.Errorf("wrong rate limit %s %d", key, limit)
		}
		calls++
		return true, nil
	}}
	s := d.Source{ID: d.ID(), Adapter: "tencent", Tenant: tencentScope, RateLimit: 30}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil || len(refs) != 102 || calls != 3 || refs[101].ExternalID != "102" {
		t.Fatalf("full scan %d, calls %d: %v", len(refs), calls, err)
	}
	for _, ref := range refs {
		if !d.CityAlternatives("深圳", ref.Locations) {
			t.Fatal("official headquarters label did not match saved city")
		}
		if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []PostingRef{refs[0], refs[101]} {
		res, err := a.FetchPosting(context.Background(), s, ref)
		if err != nil || res.Status != "SUCCESS" || !strings.Contains(res.Text, "2026年1月1日-2027年12月31日") || !strings.Contains(res.Text, "加分项：\nRedis 加分") || !strings.Contains(res.Text, "深圳总部") || strings.Contains(res.Text, "<p>") {
			t.Fatalf("original %+v %v", res, err)
		}
	}
	for _, sc := range []string{"wrong_id", "wrong_title", "wrong_project", "wrong_type", "qingyun", "missing_type", "missing_qingyun", "no_duties", "no_requirements", "next_year"} {
		scenario = sc
		if _, err := a.FetchPosting(context.Background(), s, refs[0]); err == nil {
			t.Errorf("accepted detail %s", sc)
		}
	}
	for _, sc := range []string{"next_year", "inactive", "missing_range", "missing_status", "duplicate", "intern", "third_party", "capacity", "changed_total", "truncated", "blocked", "redirect"} {
		scenario = sc
		if result, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "not found"}}); err == nil || len(result) != 0 {
			t.Errorf("accepted incomplete scope %s", sc)
		}
	}
	scenario = "empty"
	if refs, err := a.Discover(context.Background(), s, d.WatchTarget{}); err != nil || len(refs) != 0 {
		t.Fatalf("empty scope %v", err)
	}
	for _, raw := range []string{TencentCampusURL, "https://join.qq.com/"} {
		v, err := a.PreviewCampus(context.Background(), raw)
		if err != nil || v.Adapter != "tencent" || v.ProjectCode != tencentScope || v.MinimumInterval != 1800 {
			t.Fatalf("preview %+v %v", v, err)
		}
	}
	for _, raw := range []string{TencentCampusURL + "?projectId=2", TencentCampusURL + "#intern", "https://join.qq.com/post_detail.html?postid=1", "https://user:secret@join.qq.com/post.html"} {
		if RecognizeCampusURL(raw) == nil {
			t.Errorf("accepted unsupported entry %s", raw)
		}
	}
	before := calls
	s.Tenant = "2028_1"
	if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil || calls != before {
		t.Fatal("unknown scope reached network")
	}
}

// Production transport, no proxy, candidate credentials, model calls or writes.
func TestTencentCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("explicit public read-only verification")
	}
	a := PublicPlatform{}
	s := d.Source{ID: "tencent-live", Adapter: "tencent", Tenant: tencentScope}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	cancel()
	if err != nil || len(refs) == 0 {
		t.Fatalf("live full scope %d: %v", len(refs), err)
	}
	for _, ref := range refs {
		if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		res, err := a.FetchPosting(ctx, s, ref)
		cancel()
		if err != nil || res.Status != "SUCCESS" {
			t.Fatalf("original %s: %v", ref.ExternalID, err)
		}
		t.Logf("original %s %s (%d bytes)", ref.ExternalID, ref.Title, len(res.Text))
	}
	t.Log(fmt.Sprintf("full 2027 China regular campus scope %d", len(refs)))
}
