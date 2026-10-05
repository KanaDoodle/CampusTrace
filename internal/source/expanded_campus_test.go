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

func expandedSite(adapter string) CampusSite {
	for _, s := range CampusSites() {
		if s.Adapter == adapter {
			return s
		}
	}
	panic("missing fixture site")
}
func expandedTenant(adapter string) string {
	return map[string]string{"oppo": "30", "siemens": "CAMPUSRECRUITMENT", "haier": "68"}[adapter]
}
func expandedRef(adapter string, n int) PostingRef {
	id := strconv.Itoa(n)
	raw := oppoOrigin + "/university/oppo/campus/post/" + id
	if adapter == "siemens" {
		id = fmt.Sprintf("%024x", n)
		raw = siemensDetailURL(id)
	}
	if adapter == "haier" {
		raw = haierOrigin + haierDetailPath("7", id)
	}
	return PostingRef{ExternalID: id, URL: raw, Title: fmt.Sprintf("Go 工程师 %d", n)}
}
func expandedFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		site := expandedSite(adapter)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != site.URL {
			t.Error("candidate session leaked or referer missing")
		}
		if adapter == "oppo" && r.Header.Get("Tenant-Id") != "1000" {
			t.Error("missing published public tenant")
		}
		if scenario == "http_blocked" {
			w.WriteHeader(403)
			return
		}
		if scenario == "redirect" {
			http.Redirect(w, r, "https://example.org/", 302)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		write := func(data any) {
			v := map[string]any{"code": 0, "data": data}
			if scenario == "missing_code" {
				delete(v, "code")
			}
			if scenario == "blocked" {
				v["code"] = 403
			}
			json.NewEncoder(w).Encode(v)
		}
		if adapter == "oppo" {
			project := map[string]any{"idRecruitProject": 30, "projectName": oppoProjectName, "recruitmentType": "Graduate", "recruitmentTypeName": "应届生", "recruitRequire": "大陆高校：2027年1月-2027年12月；港澳台及海外：2026年1月-2027年12月"}
			if scenario == "changed_project" {
				project["projectName"] = "2028届应届生校园招聘"
			}
			if scenario == "intern_project" {
				project["recruitmentType"] = "Intern"
			}
			if strings.HasSuffix(r.URL.Path, "/project/list") {
				data := []any{project}
				if scenario == "duplicate_project" {
					data = append(data, project)
				}
				write(data)
				return
			}
			row := func(n int) map[string]any {
				return map[string]any{"idRecruitPosition": n, "projectId": 30, "projectName": oppoProjectName, "recruitmentType": "Graduate", "recruitmentTypeName": "应届生", "positionName": fmt.Sprintf("Go 工程师 %d", n), "workCityName": "深圳市,东莞市", "positionDesc": "<p>开发 Go 服务</p>", "positionRequire": "熟悉 Linux；Java/C/C++/Go 任一", "knowledgeSkill": "熟悉数据库事务", "aiCapabilityLevelDesc": "理解模型 API 集成", "bonusItem": "Redis 经验加分"}
			}
			if strings.HasSuffix(r.URL.Path, "/detail") {
				id, _ := strconv.Atoi(r.URL.Query().Get("id"))
				v := row(id)
				v["recruitmentType"] = nil
				v["workCityName"] = nil
				v["workCityVOList"] = []any{map[string]any{"workCityName": "深圳市"}}
				if scenario == "wrong_id" {
					v["idRecruitPosition"] = id + 1
				}
				if scenario == "detail_intern" {
					v["projectId"] = 29
				}
				if scenario == "empty_requirements" {
					v["positionRequire"] = "<p> </p>"
				}
				write(v)
				return
			}
			if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/pageNew") {
				t.Error("unexpected OPPO query")
			}
			var input struct {
				Page     int    `json:"pageNum"`
				Size     int    `json:"pageSize"`
				Name     string `json:"positionName"`
				Share    string `json:"shareId"`
				Projects []struct {
					ID   int    `json:"projectId"`
					Type string `json:"recruitmentType"`
					All  string `json:"isAllNode"`
				} `json:"projectList"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.Size != 50 || input.Name != "" || input.Share != "" || len(input.Projects) != 1 || input.Projects[0].ID != 30 || input.Projects[0].Type != "Graduate" || input.Projects[0].All != "Y" {
				t.Error("scope lost or filtered partial query")
			}
			total := 53
			rows := []any{}
			for n := (input.Page-1)*50 + 1; n <= min(input.Page*50, total); n++ {
				rows = append(rows, row(n))
			}
			switch scenario {
			case "empty":
				total = 0
				rows = []any{}
			case "capacity":
				total = 501
			case "partial":
				rows = rows[:len(rows)-1]
			case "drift":
				if input.Page == 2 {
					total = 54
				}
			case "duplicate":
				if input.Page == 2 {
					rows[0] = row(1)
				}
			case "list_intern":
				rows[0].(map[string]any)["recruitmentType"] = "Intern"
			case "wrong_project":
				rows[0].(map[string]any)["projectId"] = 29
			}
			data := map[string]any{"total": total, "current": input.Page, "size": 50, "pages": (total + 49) / 50, "records": rows}
			if scenario == "missing_total" {
				delete(data, "total")
			}
			if scenario == "wrong_page" {
				data["current"] = input.Page + 1
			}
			if scenario == "wrong_size" {
				data["size"] = 10
			}
			if scenario == "wrong_pages" {
				data["pages"] = 99
			}
			write(data)
			return
		}
		if adapter == "siemens" {
			w.Header().Set("Content-Type", "text/html;charset=UTF-8")
			if strings.HasSuffix(r.URL.Path, "/detail") {
				id := r.URL.Query().Get("positionId")
				scope := siemensScope
				status := "PUBLISHING"
				text := "职责：开发 Go 服务。要求：英语熟练，熟悉 Linux。"
				if scenario == "wrong_id" {
					id = fmt.Sprintf("%024x", 999)
				}
				if scenario == "detail_intern" {
					scope = "INTERNSHIPRECRUITMENT"
				}
				if scenario == "closed" {
					status = "CLOSED"
				}
				if scenario == "empty_requirements" {
					text = ""
				}
				fmt.Fprintf(w, `<script>staticParams.detailRecruitmentType = "%s";</script><input id="positionId" value="%s"><input id="positionStatus" value="%s"><div class="position-header"><h4 class="name">Go 工程师</h4></div><div id="positionDetail"><ul><li>工作经验：1年以下</li></ul><div class="position-description">%s</div></div>`, scope, id, status, text)
				return
			}
			r.ParseForm()
			offset, _ := strconv.Atoi(r.Form.Get("offset"))
			page := offset/15 + 1
			if r.Method != "POST" || r.Form.Get("recruitmentType") != siemensScope || r.Form.Get("max") != "15" || r.Form.Get("keyword") != "" {
				t.Error("unscoped Siemens query")
			}
			total := 17
			count := min(15, total-offset)
			active := page
			pages := 2
			if scenario == "empty" {
				total = 0
				count = 0
			}
			if scenario == "capacity" {
				total = 501
			}
			if scenario == "partial" {
				count--
			}
			if scenario == "drift" && page == 2 {
				total = 18
			}
			if scenario == "wrong_page" {
				active++
			}
			if scenario == "wrong_pages" {
				pages = 99
			}
			if scenario != "missing_total" {
				fmt.Fprintf(w, `<div class="card-header"><div class="txt">共%d个职位</div></div>`, total)
			}
			for n := offset + 1; n <= offset+count; n++ {
				id := fmt.Sprintf("%024x", n)
				scope := siemensScope
				if scenario == "duplicate" && page == 2 && n == 16 {
					id = fmt.Sprintf("%024x", 1)
				}
				if scenario == "list_intern" && n == 1 {
					scope = "INTERNSHIPRECRUITMENT"
				}
				fmt.Fprintf(w, `<div data-action="positionItem" pid="%s" recruitment="%s"><div class="position-name"><h4><span class="txt">Go 工程师 %d</span></h4></div><ul><li>工作地点：上海</li></ul></div>`, id, scope, n)
			}
			fmt.Fprintf(w, `<ul id="pageTurnUl"><li class="page-item disabled">共%d页</li><li class="page-item active">%d</li></ul>`, pages, active)
			return
		}
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if strings.Contains(r.URL.Path, "/activity/") {
				name := "海尔集团2027校园招聘"
				scope := "68"
				if scenario == "changed_project" {
					name = "海尔集团2026校园招聘"
				}
				if scenario == "wrong_scope_link" {
					scope = "66"
				}
				fmt.Fprintf(w, `<div class="activity_name">%s</div><a href="/client/campusmobile/researchlist/id/%s/fid/7.html">软件研发类</a>`, name, scope)
				return
			}
			parts := strings.Split(r.URL.Path, "/")
			id := strings.TrimSuffix(parts[len(parts)-1], ".html")
			scope := "68"
			text := "2027 应届毕业生，熟悉 Go；Redis 加分。"
			if scenario == "wrong_id" {
				id = "999"
			}
			if scenario == "detail_intern" {
				scope = "66"
			}
			if scenario == "empty_requirements" {
				text = ""
			}
			fmt.Fprintf(w, `<div class="title"><div class="span">Go 工程师</div><div id="collect_btn" data-rid="%s" data-aid="%s"></div></div><div id="details_box"><div class="item_div"><div class="title">岗位描述</div><div class="text">开发 Go 服务</div></div><div class="item_div"><div class="title">岗位要求</div><div class="text">%s</div></div><div class="item_div"><div class="title">工作地点</div><div class="text">青岛市</div></div></div><div class="department_box">数字科技生态圈</div>`, id, scope, text)
			return
		}
		r.ParseForm()
		page, _ := strconv.Atoi(r.Form.Get("page"))
		if r.Form.Get("id") != "68" || r.Form.Get("fid") != "" || r.Form.Get("keyword") != "" || r.Form.Get("pagesize") != "50" || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			t.Error("unscoped Haier query")
		}
		total := 52
		terminal := 0
		rows := []any{}
		if scenario == "capacity" {
			total = 501
		}
		if scenario == "empty" {
			total = 0
		}
		if page*50 >= total {
			terminal = 1
		}
		for n := (page-1)*50 + 1; n <= min(page*50, total); n++ {
			id := strconv.Itoa(n)
			path := haierDetailPath("7", id)
			if scenario == "duplicate" && page == 2 && n == 51 {
				id = "1"
				path = haierDetailPath("7", id)
			}
			if scenario == "wrong_project" {
				path = strings.Replace(path, "/id/68/", "/id/66/", 1)
			}
			rows = append(rows, map[string]any{"id": id, "function_id": "7", "name": fmt.Sprintf("Go 工程师 %d", n), "addr": "青岛市", "click_url": path})
		}
		if scenario == "partial" {
			rows = rows[:len(rows)-1]
		}
		data := map[string]any{"list": rows, "maxPage": terminal, "activity_stop": false}
		if scenario == "missing_terminal" {
			delete(data, "maxPage")
		}
		if scenario == "bad_terminal" {
			data["maxPage"] = 2
		}
		if scenario == "missing_stopped" {
			delete(data, "activity_stop")
		}
		if scenario == "stopped" {
			data["activity_stop"] = true
		}
		json.NewEncoder(w).Encode(map[string]any{"status": 1, "data": data})
	}))
}
func TestExpandedCampusCompleteScopeAndOriginal(t *testing.T) {
	for _, adapter := range []string{"oppo", "siemens", "haier"} {
		t.Run(adapter, func(t *testing.T) {
			srv := expandedFixture(t, adapter, "")
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(expandedSite(adapter).URL)
			jar.SetCookies(u, []*http.Cookie{{Name: "PRIVATE", Value: "must-not-copy"}})
			calls := 0
			a := PublicPlatform{Client: &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}, Allow: func(_ context.Context, key string, limit int) (bool, error) {
				calls++
				if key != adapter+":public-site" || limit != 30 {
					t.Error("pacing not shared")
				}
				return true, nil
			}}
			site := expandedSite(adapter)
			v, err := a.PreviewCampus(context.Background(), site.URL)
			total := map[string]int{"oppo": 53, "siemens": 17, "haier": 52}[adapter]
			if err != nil || v.Total != total || v.ProjectCode != expandedTenant(adapter) || v.MinimumInterval != 1800 || len(v.Samples) != 5 || v.SupportsDirection {
				t.Fatalf("preview %+v %v", v, err)
			}
			s := d.Source{ID: "fixture", Adapter: adapter, Tenant: v.ProjectCode}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(refs) != total {
				t.Fatalf("complete %d %v", len(refs), err)
			}
			last := refs[len(refs)-1]
			filtered, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: fmt.Sprintf("工程师 %d", total)}})
			if err != nil || len(filtered) != 1 || filtered[0].ExternalID != last.ExternalID {
				t.Fatalf("last page filtering %+v %v", filtered, err)
			}
			result, err := a.FetchPosting(context.Background(), s, last)
			if err != nil || result.Status != "SUCCESS" || !strings.Contains(result.Text, "Go") || strings.Contains(result.Text, "<script>") {
				t.Fatalf("original %+v %v", result, err)
			}
			if adapter == "oppo" {
				for _, part := range []string{"港澳台及海外：2026年1月-2027年12月", "数据库事务", "API 集成", "Redis 经验加分"} {
					if !strings.Contains(result.Text, part) {
						t.Errorf("lost original %s", part)
					}
				}
			}
			if adapter == "haier" && !strings.Contains(result.Text, "数字科技生态圈") {
				t.Error("lost public department")
			}
			if a.Client.Jar != jar || calls < 5 {
				t.Fatal("caller client changed or bypassed pacing")
			}
		})
	}
}
func TestExpandedCampusRejectsIncompleteResponses(t *testing.T) {
	scenarios := map[string][]string{
		"oppo":    {"changed_project", "intern_project", "duplicate_project", "missing_code", "blocked", "http_blocked", "redirect", "capacity", "partial", "drift", "duplicate", "list_intern", "wrong_project", "missing_total", "wrong_page", "wrong_size", "wrong_pages", "wrong_id", "detail_intern", "empty_requirements", "empty"},
		"siemens": {"http_blocked", "redirect", "capacity", "partial", "drift", "duplicate", "list_intern", "missing_total", "wrong_page", "wrong_pages", "wrong_id", "detail_intern", "closed", "empty_requirements", "empty"},
		"haier":   {"http_blocked", "redirect", "changed_project", "wrong_scope_link", "capacity", "partial", "duplicate", "wrong_project", "missing_terminal", "bad_terminal", "missing_stopped", "stopped", "wrong_id", "detail_intern", "empty_requirements", "empty"},
	}
	for adapter, items := range scenarios {
		for _, scenario := range items {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := expandedFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				s := d.Source{ID: "fixture", Adapter: adapter, Tenant: expandedTenant(adapter)}
				var err error
				if scenario == "wrong_id" || scenario == "detail_intern" || scenario == "closed" || scenario == "empty_requirements" {
					_, err = a.FetchPosting(context.Background(), s, expandedRef(adapter, 52))
				} else {
					_, err = a.Discover(context.Background(), s, d.WatchTarget{})
				}
				if scenario == "empty" {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				if err == nil {
					t.Fatal("unsafe result accepted")
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
}
func TestExpandedCampusStrictEntryAndDeferral(t *testing.T) {
	for _, adapter := range []string{"oppo", "siemens", "haier"} {
		t.Run(adapter, func(t *testing.T) {
			site := expandedSite(adapter)
			if RecognizeCampusURL(site.URL) != nil {
				t.Fatal("preset rejected")
			}
			u, _ := url.Parse(site.URL)
			q := u.Query()
			q.Set("shareId", "private-referral")
			u.RawQuery = q.Encode()
			if RecognizeCampusURL(u.String()) == nil {
				t.Fatal("partial referral scope accepted")
			}
			calls := 0
			a := PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected network") })}}
			s := d.Source{ID: "fixture", Adapter: adapter, Tenant: expandedTenant(adapter)}
			forged := expandedRef(adapter, 1)
			forged.URL = "https://example.org/1"
			if _, err := a.FetchPosting(context.Background(), s, forged); err == nil {
				t.Fatal("forged URL accepted")
			}
			if _, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil {
				t.Fatal("unsupported direction accepted")
			}
			s.Tenant = "intern"
			if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil || calls != 0 {
				t.Fatal("invalid scope reached network")
			}
			srv := expandedFixture(t, adapter, "")
			defer srv.Close()
			s.Tenant = expandedTenant(adapter)
			allowed := 0
			a = PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}, Allow: func(context.Context, string, int) (bool, error) { allowed++; return allowed < 2, nil }}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			var e *FetchError
			if len(refs) != 0 || !AsFetchError(err, &e) || e.Category != "RATE_LIMIT" || !e.Retryable {
				t.Fatalf("partial import on pacing %+v %v", refs, err)
			}
		})
	}
}
func TestExpandedCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("opt-in public website verification")
	}
	for _, adapter := range []string{"oppo", "siemens", "haier"} {
		t.Run(adapter, func(t *testing.T) {
			a := PublicPlatform{}
			site := expandedSite(adapter)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			preview, err := a.PreviewCampus(ctx, site.URL)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			s := d.Source{ID: "live-" + adapter, Adapter: adapter, Tenant: preview.ProjectCode}
			ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
			refs, err := a.Discover(ctx, s, d.WatchTarget{})
			cancel()
			if err != nil || len(refs) == 0 || len(refs) != preview.Total {
				t.Fatalf("complete %d of %d %v", len(refs), preview.Total, err)
			}
			for _, r := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "original public posting", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range []PostingRef{refs[0], refs[len(refs)-1]} {
				ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
				result, err := a.FetchPosting(ctx, s, r)
				cancel()
				if err != nil || result.Status != "SUCCESS" || len(result.Text) < 100 {
					t.Fatalf("detail %s %+v %v", r.ExternalID, result, err)
				}
				t.Logf("detail id=%s bytes=%d", r.ExternalID, len(result.Text))
			}
			t.Logf("company=%s complete_count=%d first=%s last=%s", site.Company, len(refs), refs[0].ExternalID, refs[len(refs)-1].ExternalID)
		})
	}
}
