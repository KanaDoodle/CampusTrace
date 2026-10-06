package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func gbitsFixture(t *testing.T, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != GBitsCampusURL {
			t.Error("lost anonymous public query")
		}
		if scenario == "redirect" {
			http.Redirect(w, r, "https://login.example.invalid/", 302)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) {
			v := map[string]any{"status": 10010, "success": true, "data": data}
			if scenario == "bad_status" {
				v["status"] = 10011
			}
			if scenario == "missing_status" {
				delete(v, "status")
			}
			if scenario == "bad_success" {
				v["success"] = false
			}
			json.NewEncoder(w).Encode(v)
		}
		if strings.HasSuffix(r.URL.Path, "/queryProjectList") {
			if len(body) != 0 {
				t.Error("unexpected project filter")
			}
			project := map[string]any{"projectId": gbitsProject, "projectName": gbitsProjectName}
			if scenario == "wrong_project" {
				project["projectName"] = "2026届秋季校园招聘"
			}
			projects := []any{project}
			if scenario == "duplicate_project" {
				projects = append(projects, project)
			}
			write(projects)
			return
		}
		row := func(n int, detail bool) map[string]any {
			j := map[string]any{"id": fmt.Sprintf("%032x", n), "postName": fmt.Sprintf("Go 工程师 %d", n), "recruitProjectId": gbitsProject, "recruitProjectName": gbitsProjectName, "recruitmentType": "校招正式岗位招聘", "jobStatus": "正常", "isExternal": true, "workAddress": "厦门/深圳", "description": "【岗位投递说明】提前稳定实习3个月\n【岗位职责】开发 Go 服务\n【任职要求】Go、Linux\n【加分项】Redis 加分", "postQuestionJSON": "private application question", "userName": "private candidate name"}
			if scenario == "mixed_scope" || detail && scenario == "detail_scope" {
				j["recruitProjectId"] = strings.Repeat("0", 32)
			}
			if scenario == "wrong_kind" {
				j["recruitmentType"] = "实习生招聘"
			}
			if scenario == "inactive" {
				j["jobStatus"] = "关闭"
			}
			if scenario == "private" {
				j["isExternal"] = false
			}
			if detail && scenario == "detail_title" {
				j["postName"] = "其他岗位"
			}
			if detail && scenario == "detail_id" {
				j["id"] = fmt.Sprintf("%032x", 999)
			}
			if detail && scenario == "empty_original" {
				j["description"] = "<p> </p>"
			}
			return j
		}
		if strings.HasSuffix(r.URL.Path, "/getPostDetail") {
			id, _ := body["postId"].(string)
			n, err := strconv.ParseInt(id, 16, 32)
			if len(body) != 1 || err != nil || n <= 0 {
				t.Error("wrong detail ID")
			}
			write(row(int(n), true))
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/queryRecuitPost") || body["recruitsType"] != "CAMPUS_RECRUITING" || body["recruitProjectId"] != gbitsProject || body["pageSize"] != float64(50) || body["postName"] != nil || body["recruitmentType"] != nil || body["sortField"] != "sortOrder" || body["isDescOrder"] != true {
			t.Error("wrong public scope or pagination")
		}
		page := int(body["currentPage"].(float64))
		total := 51
		if scenario == "capacity" {
			total = 501
		}
		if scenario == "empty" {
			total = 0
		}
		if scenario == "drift" && page > 1 {
			total++
		}
		jobs := []any{}
		for n := (page-1)*50 + 1; n <= min(page*50, total); n++ {
			index := n
			if scenario == "duplicate" && page > 1 {
				index = 1
			}
			jobs = append(jobs, row(index, false))
		}
		if scenario == "truncated" {
			jobs = jobs[:max(0, len(jobs)-1)]
		}
		data := map[string]any{"count": total, "list": jobs}
		if scenario == "missing_total" {
			delete(data, "count")
		}
		write(data)
	}))
}

func TestGBitsFullScopeAnonymousAndOriginal(t *testing.T) {
	srv := gbitsFixture(t, "")
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(gbitsOrigin)
	jar.SetCookies(u, []*http.Cookie{{Name: "candidate_session", Value: "private"}})
	client := &http.Client{Transport: rewriteTransport{srv.URL}, Jar: jar}
	calls := 0
	a := PublicPlatform{Client: client, Allow: func(context.Context, string, int) (bool, error) { calls++; return true, nil }}
	s := d.Source{ID: d.ID(), Adapter: "gbits", Tenant: gbitsProject, RateLimit: 30}
	v, err := a.PreviewCampus(context.Background(), GBitsCampusURL)
	if err != nil || v.Total != 51 || v.ProjectCode != gbitsProject || v.MinimumInterval != 1800 {
		t.Fatalf("preview %+v %v", v, err)
	}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil || len(refs) != 51 {
		t.Fatalf("full scan %d %v", len(refs), err)
	}
	for _, r := range refs {
		if r.JobType != "UNKNOWN" || len(r.Locations) != 2 {
			t.Fatal("invented employment type or lost city")
		}
		if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []PostingRef{refs[0], refs[len(refs)-1]} {
		v, err := a.FetchPosting(context.Background(), s, r)
		if err != nil || v.Status != "SUCCESS" || !strings.Contains(v.Text, "Redis 加分") || !strings.Contains(v.Text, "实习3个月") || strings.Contains(v.Text, "private") {
			t.Fatalf("original %+v %v", v, err)
		}
	}
	selected, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "Go 工程师 51"}})
	if err != nil || len(selected) != 1 || selected[0].ExternalID != refs[50].ExternalID {
		t.Fatalf("tail keyword %v %v", selected, err)
	}
	if client.Jar != jar || calls < 10 {
		t.Fatal("client mutated or pacing skipped")
	}
}

func TestGBitsRejectsIncompleteOrMixedResults(t *testing.T) {
	for _, scenario := range []string{"wrong_project", "duplicate_project", "mixed_scope", "wrong_kind", "inactive", "private", "bad_status", "missing_status", "bad_success", "truncated", "duplicate", "drift", "capacity", "missing_total", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			srv := gbitsFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			refs, err := a.Discover(context.Background(), d.Source{ID: d.ID(), Adapter: "gbits", Tenant: gbitsProject}, d.WatchTarget{})
			if err == nil || len(refs) != 0 {
				t.Fatalf("partial or wrong scope %d %v", len(refs), err)
			}
		})
	}
	for _, scenario := range []string{"detail_scope", "detail_title", "detail_id", "empty_original"} {
		t.Run(scenario, func(t *testing.T) {
			srv := gbitsFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{ID: d.ID(), Adapter: "gbits", Tenant: gbitsProject}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil {
				t.Fatal(err)
			}
			v, err := a.FetchPosting(context.Background(), s, refs[0])
			if err == nil || v.Status == "SUCCESS" {
				t.Fatal("wrong detail accepted")
			}
		})
	}
}

func TestGBitsEmptyDeferredAndStrictEntry(t *testing.T) {
	srv := gbitsFixture(t, "empty")
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	v, err := a.PreviewCampus(context.Background(), GBitsCampusURL)
	if err != nil || v.Total != 0 {
		t.Fatalf("empty %+v %v", v, err)
	}
	full := gbitsFixture(t, "")
	defer full.Close()
	calls := 0
	a = PublicPlatform{Client: &http.Client{Transport: rewriteTransport{full.URL}}, Allow: func(context.Context, string, int) (bool, error) { calls++; return calls < 3, nil }}
	refs, err := a.Discover(context.Background(), d.Source{ID: d.ID(), Adapter: "gbits", Tenant: gbitsProject}, d.WatchTarget{})
	var fetchErr *FetchError
	if len(refs) != 0 || !AsFetchError(err, &fetchErr) || fetchErr.Category != "RATE_LIMIT" || !fetchErr.Retryable {
		t.Fatalf("partial deferred scan %v %v", refs, err)
	}
	for _, raw := range []string{GBitsCampusURL + "?shareId=private", strings.Replace(GBitsCampusURL, gbitsProject, "other", 1), strings.Replace(GBitsCampusURL, "https:", "http:", 1)} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("unsupported entry accepted")
		}
	}
	if _, err := PlatformURL(d.Source{Adapter: "gbits", Tenant: "intern"}); err == nil {
		t.Fatal("wrong source scope accepted")
	}
}
