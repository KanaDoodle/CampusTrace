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

var bankAdapters = []string{"cmb_tech", "citic_tech", "boc_software", "boc_operations"}

func bankFixtureRow(adapter string, n int, city string) map[string]any {
	switch adapter {
	case "cmb_tech":
		title := fmt.Sprintf("信息技术培养生 %d", n)
		if n < 51 {
			title = fmt.Sprintf("客户经理 %d", n)
		}
		if n == 1 {
			title = "航运租赁技术岗"
		}
		return map[string]any{"publishGID": fmt.Sprintf("00000000-0000-0000-0000-%012d", n), "jobDisplay": title, "branchCode": "100001", "branchCodeName": "信息技术部", "locationName": "深圳", "recruitmentTypeID": cmbGraduate, "jobResponsibility": "<p>开发 Go 服务；含柜面轮岗</p>", "jobRequirement": "2027届应届生；Go、Linux；英语六级", "expiredOn": "2026-09-30"}
	case "citic_tech":
		return map[string]any{"ID": n, "RELEASENAME": fmt.Sprintf("信息科技类(A%06d)", n), "CONTENT": "总行", "WORKADDR": "北京"}
	default:
		return map[string]any{"id": fmt.Sprintf("%024x", n), "name": fmt.Sprintf("信息科技岗 %d", n), "companyId": city, "companyName": bocUnits[adapter].Cities[city], "address": "北京", "experience": "应届毕业生", "isShow": 1, "education": "本科", "jobDesc": `职位介绍：\n开发 Go 服务\n招聘条件及要求：\nGo、Linux；其他条件见公告`, "applyEndTime": int64(1791561600000)}
	}
}

// Synthetic public responses: full pages, original text, and malformed variants.
func bankFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != expandedSite(adapter).URL {
			t.Error("public request leaked session or lost referer")
		}
		if scenario == "redirect" {
			http.Redirect(w, r, "https://login.example.invalid/", 302)
			return
		}
		if scenario == "http_blocked" {
			w.WriteHeader(403)
			return
		}
		write := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		q := r.URL.Query()
		if adapter == "cmb_tech" || adapter == "citic_tech" {
			if strings.Contains(r.URL.Path, "getDetail") || strings.Contains(r.URL.Path, "positionDetail_") {
				n := 51
				if adapter == "cmb_tech" {
					row := bankFixtureRow(adapter, n, "")
					switch scenario {
					case "detail_scope":
						row["recruitmentTypeID"] = "intern"
					case "detail_id":
						row["publishGID"] = fmt.Sprintf("00000000-0000-0000-0000-%012d", 999)
					case "detail_title":
						row["jobDisplay"] = "其他岗位"
					case "detail_unit":
						row["branchCodeName"] = "其他分行"
					case "empty_duties":
						row["jobResponsibility"] = ""
					case "empty_requirements":
						row["jobRequirement"] = ""
					}
					if q.Get("publishId") != fmt.Sprintf("00000000-0000-0000-0000-%012d", n) {
						t.Error("wrong detail id")
					}
					write(map[string]any{"returnCode": "SUC0000", "body": row})
				} else {
					id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/static/positionDetail_"), "_02.html")
					n, _ = strconv.Atoi(id)
					title := fmt.Sprintf("信息科技类(A%06d)", n)
					fields := map[string]string{"postName2": title, "WORKADDR": "北京", "ZY": "计算机相关", "XL": "本科及以上", "GZLX": "全职", "JOBDUTY": "开发 Go 服务；含基层培养", "RZYQ": "2027届应届生；Go、Linux；英语六级"}
					switch scenario {
					case "detail_title":
						fields["postName2"] = "其他岗位"
					case "detail_scope":
						fields["RZYQ"] = "三年工作经验"
					case "empty_duties":
						fields["JOBDUTY"] = ""
					case "empty_requirements":
						fields["RZYQ"] = ""
					case "login":
						fmt.Fprint(w, "<html>请登录</html>")
						return
					}
					for k, v := range fields {
						fmt.Fprintf(w, "<div id=%q>%s</div>", k, v)
					}
				}
				return
			}
			var body map[string]any
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("wrong public query")
				w.WriteHeader(400)
				return
			}
			page, size, total := 1, 50, 51
			if adapter == "cmb_tech" {
				page = int(body["pageIndex"].(float64))
				if body["recruitmentTypeId"] != cmbGraduate || body["keywords"] != "" || body["pageSize"] != float64(50) || len(body["orgIdList"].([]any)) != 0 || len(body["locationIdList"].([]any)) != 0 {
					t.Error("graduate scope lost")
				}
			} else {
				page = int(body["page"].(float64))
				size, total = 15, 16
				if body["recruitmentType"] != "02" || body["RELEASENAME"] != "信息科技" || body["userId"] != nil {
					t.Error("campus IT scope lost")
				}
			}
			if scenario == "capacity" {
				total = 501
			}
			if scenario == "empty" {
				total = 0
			}
			rows := []any{}
			for n := (page-1)*size + 1; n <= min(page*size, total); n++ {
				id := n
				if scenario == "duplicate" && n == total {
					id = 1
				}
				row := bankFixtureRow(adapter, id, "")
				if adapter == "citic_tech" {
					if scenario == "mixed_scope" {
						row["RELEASENAME"] = "客户经理"
					}
					rows = append(rows, map[string]any{"itemMap": row})
				} else {
					rows = append(rows, row)
				}
			}
			if scenario == "truncated" && page == 2 {
				rows = []any{}
			}
			if scenario == "drift" && page == 2 {
				total++
			}
			var response map[string]any
			if adapter == "cmb_tech" {
				data := map[string]any{"total": total, "data": rows}
				if scenario == "missing_total" {
					delete(data, "total")
				}
				response = map[string]any{"returnCode": "SUC0000", "body": data}
				if scenario == "missing_code" {
					delete(response, "returnCode")
				}
			} else {
				response = map[string]any{"IsSuc": true, "pageCount": total, "tableData": map[string]any{"rows": rows}}
				if scenario == "missing_total" {
					delete(response, "pageCount")
				}
				if scenario == "missing_code" {
					delete(response, "IsSuc")
				}
			}
			write(response)
			return
		}
		unit := bocUnits[adapter]
		if r.URL.Path == "/api/company/list" {
			if q.Get("token") != bocPublicProject || q.Get("pageSize") != "10000" {
				t.Error("wrong public project")
			}
			row := func(id, name, parent string) map[string]any {
				return map[string]any{"id": id, "name": name, "parentId": parent, "projectId": bocProject}
			}
			root := row(bocRoot, "中国银行2027年全球校园招聘", "")
			if scenario == "wrong_year" {
				root["name"] = "中国银行2026年全球校园招聘"
			}
			rows := []any{root, row(unit.ID, unit.Name, bocRoot)}
			for id, name := range unit.Cities {
				rows = append(rows, row(id, name, unit.ID))
			}
			if scenario == "missing_city" {
				rows = rows[:len(rows)-1]
			}
			if scenario == "duplicate_catalog" {
				rows = append(rows, rows[0])
			}
			if scenario == "wrong_project" {
				root["projectId"] = "other"
			}
			write(map[string]any{"code": 1, "retMsg": rows})
			return
		}
		city := q.Get("companyId")
		n := 1
		if strings.Contains(r.URL.Path, "/api/job/detail/") {
			id := strings.TrimPrefix(r.URL.Path, "/api/job/detail/")
			v, _ := strconv.ParseInt(id, 16, 32)
			n = int(v)
			for k := range unit.Cities {
				v, _ := strconv.ParseInt(k[len(k)-3:], 16, 32)
				if int(v) == n {
					city = k
				}
			}
			row := bankFixtureRow(adapter, n, city)
			switch scenario {
			case "detail_id":
				row["id"] = fmt.Sprintf("%024x", 999)
			case "detail_title":
				row["name"] = "其他岗位"
			case "detail_unit":
				row["companyName"] = "其他单位"
			case "detail_scope":
				row["experience"] = "三年以上"
			case "empty_duties":
				row["jobDesc"] = "职位介绍：招聘条件及要求：Go"
			case "empty_requirements":
				row["jobDesc"] = "职位介绍：开发 Go 服务招聘条件及要求："
			}
			write(map[string]any{"code": 1, "retMsg": row})
			return
		}
		if r.URL.Path != "/api/job/list" || unit.Cities[city] == "" || q.Get("token") != bocPublicProject || q.Get("pageSize") != "50" || q.Get("page") != "1" {
			t.Error("wrong unit query")
		}
		v, _ := strconv.ParseInt(city[len(city)-3:], 16, 32)
		n = int(v)
		row := bankFixtureRow(adapter, n, city)
		switch scenario {
		case "mixed_scope":
			row["experience"] = "不限"
		case "unknown_kind":
			delete(row, "isShow")
		case "wrong_unit":
			row["companyId"] = unit.ID
		case "duplicate":
			row["id"] = fmt.Sprintf("%024x", 1)
		}
		response := map[string]any{"code": 1, "retMsg": []any{row}, "totalCount": 1}
		if scenario == "truncated" {
			response["totalCount"] = 2
		}
		if scenario == "capacity" {
			response["totalCount"] = 501
		}
		if scenario == "missing_total" {
			delete(response, "totalCount")
		}
		if scenario == "missing_code" {
			delete(response, "code")
		}
		if scenario == "empty" {
			response["retMsg"] = []any{}
			response["totalCount"] = 0
		}
		write(response)
	}))
}

func TestBankCampusFullScopeAndOriginals(t *testing.T) {
	for _, adapter := range bankAdapters {
		t.Run(adapter, func(t *testing.T) {
			srv := bankFixture(t, adapter, "")
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(expandedSite(adapter).URL)
			jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
			client := &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}
			key := adapter + ":public-site"
			if strings.HasPrefix(adapter, "boc_") {
				key = "boc:public-site"
			}
			a := PublicPlatform{Client: client, Allow: func(_ context.Context, k string, limit int) (bool, error) {
				if k != key || limit != 30 {
					t.Error("site pacing lost")
				}
				return true, nil
			}}
			want := 1
			if adapter == "citic_tech" {
				want = 16
			}
			if strings.HasPrefix(adapter, "boc_") {
				want = len(bocUnits[adapter].Cities)
			}
			preview, err := a.PreviewCampus(context.Background(), expandedSite(adapter).URL)
			if err != nil || preview.Total != want || preview.MinimumInterval != 1800 {
				t.Fatalf("preview %+v %v", preview, err)
			}
			if preview.Name != expandedSite(adapter).Company+" · 校招" || len(preview.Name) > 160 {
				t.Fatal("bank preview name cannot be saved")
			}
			s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(refs) != want {
				t.Fatalf("complete scope %d %v", len(refs), err)
			}
			for _, ref := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
				res, err := a.FetchPosting(context.Background(), s, ref)
				if err != nil || res.Status != "SUCCESS" || !strings.Contains(res.Text, "Go") || !strings.Contains(res.Text, "Linux") || strings.Contains(res.Text, "<p>") {
					t.Fatalf("original %+v %v", res, err)
				}
				if adapter == "cmb_tech" && (!strings.Contains(res.Text, "柜面轮岗") || !strings.Contains(res.Text, "2026-09-30")) {
					t.Fatal("training or expired deadline silently removed")
				}
				if adapter == "citic_tech" && !strings.Contains(res.Text, "英语六级") {
					t.Fatal("qualification lost")
				}
				if strings.HasPrefix(adapter, "boc_") && (!strings.Contains(res.Text, bocConditionsURL) || !strings.Contains(res.Text, "未在本岗位正文展开") || !strings.Contains(res.Text, "北京时间")) {
					t.Fatal("external qualification reference or deadline lost")
				}
			}
			selected, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: refs[len(refs)-1].Title}})
			if err != nil || len(selected) != 1 {
				t.Fatal("keyword filtering lost last page")
			}
			if client.Jar != jar {
				t.Fatal("caller client changed")
			}
		})
	}
}
func TestBankCampusRejectsUnverifiableLists(t *testing.T) {
	for _, adapter := range bankAdapters {
		scenarios := []string{"truncated", "duplicate", "capacity", "missing_total", "missing_code", "redirect", "http_blocked"}
		if adapter == "citic_tech" {
			scenarios = append(scenarios, "mixed_scope", "drift")
		}
		if adapter == "cmb_tech" {
			scenarios = append(scenarios, "drift")
		}
		if strings.HasPrefix(adapter, "boc_") {
			scenarios = append(scenarios, "mixed_scope", "unknown_kind", "wrong_unit", "wrong_year", "wrong_project", "missing_city", "duplicate_catalog")
		}
		for _, scenario := range scenarios {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := bankFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				refs, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
				if err == nil || refs != nil {
					t.Fatalf("unverified or partial scope accepted %d %v", len(refs), err)
				}
			})
		}
	}
}
func TestBankCampusRejectsWrongAndEmptyDetails(t *testing.T) {
	for _, adapter := range bankAdapters {
		scenarios := []string{"detail_scope", "detail_title", "empty_duties", "empty_requirements"}
		if adapter != "citic_tech" {
			scenarios = append(scenarios, "detail_id", "detail_unit")
		} else {
			scenarios = append(scenarios, "login")
		}
		for _, scenario := range scenarios {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := bankFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				s := d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}
				refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
				if err != nil {
					t.Fatal(err)
				}
				res, err := a.FetchPosting(context.Background(), s, refs[0])
				if err == nil || res.Status == "SUCCESS" {
					t.Fatal("wrong detail accepted")
				}
			})
		}
	}
}
func TestBankCampusEmptyScopeAndEntryGuards(t *testing.T) {
	for _, adapter := range bankAdapters {
		t.Run(adapter, func(t *testing.T) {
			site := expandedSite(adapter)
			if RecognizeCampusURL(site.URL) != nil {
				t.Fatal("preset rejected")
			}
			srv := bankFixture(t, adapter, "empty")
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			refs, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
			if err != nil || refs == nil || len(refs) != 0 {
				t.Fatal("valid empty scope rejected")
			}
			if !d.IsCampusSource(adapter) {
				t.Fatal("scheduler source unknown")
			}
			u, _ := url.Parse(site.URL)
			q := u.Query()
			q.Set("inviteCode", "private")
			u.RawQuery = q.Encode()
			if RecognizeCampusURL(u.String()) == nil {
				t.Fatal("candidate query accepted")
			}
			calls := 0
			a = PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected") })}}
			s := d.Source{Adapter: adapter, Tenant: "intern"}
			if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil || calls != 0 {
				t.Fatal("wrong scope reached network")
			}
			s.Tenant = moreTenant(adapter)
			if _, err := a.FetchPosting(context.Background(), s, PostingRef{ExternalID: "1", URL: "https://private.invalid/"}); err == nil || calls != 0 {
				t.Fatal("forged detail reached network")
			}
		})
	}
}
