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

var tclCECAdapters = []string{"tcl_digital", "tcl_honghu", "cec_software"}

func tclCECFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != expandedURL(adapter) {
			t.Error("public query inherited credentials or lost official referer")
		}
		if scenario == "redirect" {
			http.Redirect(w, r, "https://login.example.invalid/", 302)
			return
		}
		if scenario == "blocked" {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(v any) {
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		if adapter != "cec_software" {
			if r.Header.Get("Origin") != tclOrigin || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Error("missing official anonymous AJAX headers")
			}
			unit := tclUnits[adapter]
			if r.URL.Path == "/campus/recruiting.html" {
				if r.Method != "GET" || r.URL.Query().Get("id") != unit.Page {
					t.Error("wrong official unit page")
				}
				w.Header().Set("Content-Type", "text/html")
				project, unitID := tclProject, unit.ID
				if scenario == "project" {
					project = "2026"
				}
				if scenario == "unit_config" {
					unitID = "999"
				}
				fmt.Fprintf(w, `<label>TCL 2027届全球校园招聘 面向毕业时间为2026年9月-2027年12月的全球应届毕业生<input name="job_xm[]" value="%s"></label><label>%s<input name="cy_type[]" id="cy_type_%s" value="%s"></label>`, project, unit.Name, unit.Page, unitID)
				return
			}
			if r.Method != "POST" {
				t.Error("unexpected public query method")
			}
			if r.URL.Path == "/Ajax/job_detail.html" {
				if !tclID.MatchString(r.URL.Query().Get("postid")) || r.URL.Query().Get("recruitType") != "1" {
					t.Error("wrong detail identity or campus classification")
				}
				duty, requirement := "<p>开发 Go 服务</p>", "Go、Linux；Redis 加分"
				if scenario == "empty_original" {
					requirement = "<p> </p>"
				}
				write(map[string]any{"title": "success", "workContent": duty, "serviceCondition": requirement, "candidateName": "private"})
				return
			}
			if r.URL.Path != "/Ajax/campus_search.html" {
				t.Error("unexpected endpoint")
				return
			}
			r.ParseForm()
			if r.PostForm.Get("job_xm[]") != tclProject || r.PostForm.Get("cy_type[]") != unit.ID || r.PostForm.Get("keyType") != "1" || r.PostForm.Get("cate_id") != "100" || len(r.PostForm) != 5 {
				t.Error("wrong scope or nonpublic query")
			}
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			total := 11
			if scenario == "capacity" {
				total = 501
			}
			if scenario == "empty" {
				total = 0
			}
			if scenario == "drift" && page > 1 {
				total++
			}
			content := "<div class=\"itembox\">"
			for n := (page-1)*10 + 1; n <= min(page*10, total); n++ {
				index := n
				if scenario == "duplicate" && page > 1 {
					index = 1
				}
				id := fmt.Sprintf("%024x", index)
				name, org, kind, link := fmt.Sprintf("Go 工程师 %d", index), unit.Name, "1", tclJobURL(id)
				if scenario == "mixed_unit" {
					org = "其他 TCL 部门"
				}
				if scenario == "mixed_kind" {
					kind = "2"
				}
				if scenario == "link" {
					link = "https://private.example.invalid/"
				}
				if scenario == "detail_title" && r.PostForm.Get("keys") != "" {
					name = "其他岗位"
				}
				if scenario == "detail_id" && r.PostForm.Get("keys") != "" {
					id = fmt.Sprintf("%024x", 999)
					link = tclJobURL(id)
				}
				if scenario == "truncated" && n == min(page*10, total) {
					continue
				}
				content += fmt.Sprintf(`<div class="proInfoConList"><div class="head" data-postid="%s" data-recruitType="%s"><div class="name">%s</div><div class="tag"><span>%s</span><span>深圳市</span></div></div><a class="tool-btn" href="%s">申请</a></div>`, id, kind, name, org, link)
			}
			content += "</div>"
			shownPage := page
			if scenario == "page" {
				shownPage = 99
			}
			if total > 0 {
				content += fmt.Sprintf(`<div class="pagination"><a class="active" onclick="javascript:job_search(%d)">%d</a><a class="active next">下一页</a></div>`, shownPage, shownPage)
			}
			v := map[string]any{"title": "success", "total_counts": total, "content": content}
			if scenario == "missing_total" {
				delete(v, "total_counts")
			}
			if scenario == "status" {
				v["title"] = "failed"
			}
			write(v)
			return
		}
		writeCEC := func(data any) {
			code := "000000"
			if scenario == "status" {
				code = "error"
			}
			write(map[string]any{"code": code, "data": data})
		}
		if r.URL.Path == "/student-api/api/common/listOrg" {
			orgID := int64(579425410220035)
			if scenario == "unit_config" {
				orgID = 999
			}
			children := []any{map[string]any{"id": orgID, "name": "麒麟软件有限公司", "children": []any{}}, map[string]any{"id": 579425410220074, "name": "中电云计算技术有限公司", "children": []any{}}}
			if scenario == "duplicate_unit" {
				children = append(children, children[0])
			}
			writeCEC([]any{map[string]any{"id": 464790010986497, "name": "中国电子信息产业集团有限公司", "children": children}})
			return
		}
		row := func(n int, detail bool) map[string]any {
			orgID, org := int64(579425410220035), "麒麟软件有限公司"
			if n%2 == 0 {
				orgID, org = 579425410220074, "中电云计算技术有限公司"
			}
			kind := 0
			if scenario == "mixed_unit" || detail && scenario == "detail_unit" {
				orgID, org = 999, "其他中国电子单位"
			}
			if scenario == "mixed_kind" || detail && scenario == "detail_kind" {
				kind = 1
			}
			name, requirement := fmt.Sprintf("Go 工程师 %d", n), "Go、Linux；Redis 加分"
			if detail && scenario == "detail_title" {
				name = "其他岗位"
			}
			if detail && scenario == "empty_original" {
				requirement = "<p> </p>"
			}
			v := map[string]any{"id": strconv.Itoa(n), "name": name, "positionType": kind, "cityName": "北京、上海", "org": org, "orgName": org, "orgId": orgID, "workNature": "全职", "jobDescription": "开发 Go 服务", "jobRequirements": requirement, "salaryRequirements": "六险二金", "closeDate": "2026-11-18", "candidateName": "private"}
			if detail {
				v["id"] = n
			}
			if detail && scenario == "detail_id" {
				v["id"] = 999
			}
			if scenario == "missing_kind" {
				delete(v, "positionType")
			}
			return v
		}
		if strings.HasPrefix(r.URL.Path, "/student-api/api/position/find/") {
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/student-api/api/position/find/"))
			if r.Method != "GET" || n <= 0 {
				t.Error("wrong public detail")
			}
			writeCEC(row(n, true))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/student-api/api/position/search" {
			t.Error("unexpected endpoint")
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		units, _ := body["orgId"].([]any)
		if len(body) != 4 || body["size"] != float64(50) || body["positionType"] != "0" || len(units) != 2 || units[0] != float64(579425410220035) || units[1] != float64(579425410220074) {
			t.Error("wrong scope or pagination")
		}
		page := int(body["page"].(float64))
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
		rows := []any{}
		for n := (page-1)*50 + 1; n <= min(page*50, total); n++ {
			index := n
			if scenario == "duplicate" && page > 1 {
				index = 1
			}
			rows = append(rows, row(index, false))
		}
		if scenario == "truncated" {
			rows = rows[:max(0, len(rows)-1)]
		}
		v := map[string]any{"total": total, "current": page, "size": 50, "pages": (total + 49) / 50, "records": rows}
		if scenario == "missing_total" {
			delete(v, "total")
		}
		if scenario == "page" {
			v["current"] = 99
		}
		writeCEC(v)
	}))
}

func TestTCLCECFullScopeAndOriginal(t *testing.T) {
	for _, adapter := range tclCECAdapters {
		t.Run(adapter, func(t *testing.T) {
			srv := tclCECFixture(t, adapter, "")
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(sectorScopes[adapter].Origin)
			jar.SetCookies(u, []*http.Cookie{{Name: "candidate_session", Value: "private"}})
			keys := []string{}
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}, Jar: jar}, Allow: func(_ context.Context, key string, _ int) (bool, error) { keys = append(keys, key); return true, nil }}
			s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter), RateLimit: 30}
			want := 11
			if adapter == "cec_software" {
				want = 51
			}
			v, err := a.PreviewCampus(context.Background(), expandedURL(adapter))
			if err != nil || v.Total != want || v.Adapter != adapter || v.ProjectCode != s.Tenant || v.MinimumInterval != 1800 || len(v.Name) == 0 || len(v.Name) > 160 {
				t.Fatalf("preview %+v %v", v, err)
			}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(refs) != want {
				t.Fatalf("full scan %d %v", len(refs), err)
			}
			for _, r := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
				if adapter != "cec_software" && r.JobType != "UNKNOWN" {
					t.Fatal("campus category invented employment type")
				}
			}
			for _, r := range []PostingRef{refs[0], refs[len(refs)-1]} {
				res, err := a.FetchPosting(context.Background(), s, r)
				if err != nil || res.Status != "SUCCESS" || !strings.Contains(res.Text, "Redis 加分") || strings.Contains(res.Text, "private") {
					t.Fatalf("original %+v %v", res, err)
				}
				if adapter == "cec_software" && (!strings.Contains(res.Text, "2026-11-18") || !strings.Contains(res.Text, "六险二金")) {
					t.Fatal("lost public deadline or benefits")
				}
			}
			if adapter != "cec_software" {
				for _, key := range keys {
					if key != "tcl:public-site" {
						t.Fatal("TCL departments did not share site limit")
					}
				}
			}
		})
	}
}

func TestTCLCECRejectIncompleteOrMixedScope(t *testing.T) {
	for _, adapter := range tclCECAdapters {
		cases := []string{"redirect", "blocked", "status", "unit_config", "capacity", "drift", "duplicate", "truncated", "missing_total", "page", "mixed_kind", "mixed_unit"}
		if adapter != "cec_software" {
			cases = append(cases, "project", "link")
		} else {
			cases = append(cases, "duplicate_unit", "missing_kind")
		}
		for _, scenario := range cases {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := tclCECFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				refs, err := a.Discover(context.Background(), d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
				if err == nil || len(refs) != 0 {
					t.Fatalf("partial or wrong scope accepted %d %v", len(refs), err)
				}
			})
		}
		for _, scenario := range []string{"detail_title", "detail_id", "empty_original", "detail_unit", "detail_kind"} {
			if adapter != "cec_software" && (scenario == "detail_unit" || scenario == "detail_kind") {
				continue
			}
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := tclCECFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}
				refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = a.FetchPosting(context.Background(), s, refs[0]); err == nil {
					t.Fatal("invalid original accepted")
				}
			})
		}
		t.Run(adapter+"/empty", func(t *testing.T) {
			srv := tclCECFixture(t, adapter, "empty")
			defer srv.Close()
			v, err := (PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}).PreviewCampus(context.Background(), expandedURL(adapter))
			if err != nil || v.Total != 0 {
				t.Fatalf("empty scope %+v %v", v, err)
			}
		})
	}
}

func TestTCLCECStrictURLAndTenant(t *testing.T) {
	for _, adapter := range tclCECAdapters {
		site := expandedSite(adapter)
		if RecognizeCampusURL(site.URL) != nil {
			t.Fatal("preset missing")
		}
		for _, raw := range []string{strings.Replace(site.URL, "https://", "http://", 1), site.URL + "&shareId=private", site.URL + "#private", strings.Replace(site.URL, "https://", "https://user@", 1)} {
			if RecognizeCampusURL(raw) == nil {
				t.Fatal("unexpected URL accepted", raw)
			}
		}
		if _, err := PlatformURL(d.Source{Adapter: adapter, Tenant: "other"}); err == nil {
			t.Fatal("unknown project accepted")
		}
		if !d.IsCampusSource(adapter) || d.SourceMinimumInterval(adapter) != 1800 {
			t.Fatal("missing scheduling bound")
		}
	}
}
