package source

import (
	"context"
	"encoding/base64"
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

var sectorAdapters = []string{"ths", "cmbnt", "netease_game", "leihuo", "ctyun", "ctcloud"}

func sectorID(adapter string, n int) string {
	if adapter == "cmbnt" {
		return fmt.Sprintf("%032X", n)
	}
	return strconv.Itoa(n)
}
func sectorRow(adapter string, n int) map[string]any {
	id, title := sectorID(adapter, n), fmt.Sprintf("Go 工程师 %d", n)
	duty, req := "<p>开发 Go 服务</p>", "Go、Linux；Redis 加分"
	switch adapter {
	case "ths":
		return map[string]any{"id": n, "name": title, "apply_recruitment_series_id": 61, "apply_recruitment_series_name": thsScope, "base": "杭州", "intro": duty, "requirement": req}
	case "cmbnt":
		return map[string]any{"jobId": id, "jobName": title, "recruitType": 0, "workingPlace": "杭州/深圳", "jobExplain": duty, "jobCondition": req, "deadline": "2100-01-01"}
	case "netease_game":
		return map[string]any{"id": n, "positionName": title, "projectId": 102, "workPlaceName": "杭州", "positionDescription": duty, "positionRequirement": req}
	case "leihuo":
		return map[string]any{"ehr_job_id": id, "job_name": title, "ehr_project_id": "77", "ehr_job_type": "1", "type_name": "全职", "target": "2027届应届毕业生", "work_place_name": "杭州", "job_detail_url": "https://campus.163.com/app/detail/index?id=" + id + "&projectId=77", "job_description": duty, "job_requirement": req}
	default:
		return map[string]any{"PostId": n, "PostName": title, "WorkPlace": "北京", "OrgName": ctUnits[adapter].Name, "RecruitType": 1}
	}
}
func mutateSector(row map[string]any, adapter, scenario string) {
	name, requirement, duty := map[string]string{"ths": "name", "cmbnt": "jobName", "netease_game": "positionName", "leihuo": "job_name", "ctyun": "name", "ctcloud": "name"}[adapter], map[string]string{"ths": "requirement", "cmbnt": "jobCondition", "netease_game": "positionRequirement", "leihuo": "job_requirement", "ctyun": "serviceCondition", "ctcloud": "serviceCondition"}[adapter], map[string]string{"ths": "intro", "cmbnt": "jobExplain", "netease_game": "positionDescription", "leihuo": "job_description", "ctyun": "workConcet", "ctcloud": "workConcet"}[adapter]
	switch scenario {
	case "mixed_scope", "detail_scope":
		switch adapter {
		case "ths":
			row["apply_recruitment_series_id"] = 54
		case "cmbnt":
			row["recruitType"] = 1
		case "netease_game":
			row["projectId"] = 103
		case "leihuo":
			row["ehr_project_id"] = "73"
		default:
			row["OrgName"] = "中国电信其他单位"
		}
	case "empty_requirements":
		row[requirement] = "<p> </p>"
	case "empty_duties":
		row[duty] = ""
	case "detail_title":
		row[name] = "其他岗位"
	case "detail_id":
		switch adapter {
		case "ths", "netease_game":
			row["id"] = 999
		case "cmbnt":
			row["jobId"] = sectorID(adapter, 999)
		case "leihuo":
			row["ehr_job_id"] = "999"
		default:
			row["orgCode"] = "0/999"
		}
	case "detail_project":
		row["projectName"] = "2026年度秋季校园招聘"
	case "future_target":
		row["target"] = "27届及之后毕业"
	case "wrong_target":
		row["target"] = "2026届应届毕业生"
	case "unknown_kind":
		switch adapter {
		case "cmbnt":
			delete(row, "recruitType")
		case "leihuo":
			row["ehr_job_type"] = "2"
		default:
			delete(row, "RecruitType")
		}
	}
}
func sectorFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != expandedSite(adapter).URL {
			t.Error("anonymous public request lost")
		}
		if scenario == "http_blocked" {
			w.WriteHeader(403)
			return
		}
		if scenario == "redirect" {
			http.Redirect(w, r, "https://login.example.invalid/", 302)
			return
		}
		write := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		envelope := func(v any) map[string]any {
			switch adapter {
			case "ths":
				return map[string]any{"success": true, "erro_code": "0", "ex_data": v}
			case "cmbnt":
				return map[string]any{"type": "SUCCESS", "data": v}
			case "netease_game":
				return map[string]any{"code": 200, "data": v}
			case "leihuo":
				return map[string]any{"status": 200, "data": v}
			default:
				return map[string]any{"code": "00", "data": v}
			}
		}
		q := r.URL.Query()
		if strings.HasSuffix(r.URL.Path, "/recruitmentSeries/list") {
			if q.Get("type") != "0" {
				t.Error("wrong series category")
			}
			closed := 0
			if scenario == "closed" {
				closed = 1
			}
			write(envelope([]any{map[string]any{"id": 61, "series_name": thsScope, "cut_off": closed}}))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/project/navigation/list") {
			link := NeteaseGameCampusURL
			if scenario == "closed" {
				link = NeteaseCampusURL
			}
			write(envelope([]any{map[string]any{"title": "应届生", "children": []any{map[string]any{"title": "网易互娱2027届校园招聘", "link": link}}}}))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/getNavMenu") {
			project := ctProject
			if scenario == "closed" {
				project = "2026"
			}
			write(envelope([]any{map[string]any{"name": "校园招聘", "path": "/", "children": []any{map[string]any{"name": ctProjectName, "params": `{"recruitProject":"` + project + `"}`}}}}))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/getShowRecruitOrg") {
			if q.Get("orgId") != ctUnits[adapter].ID || q.Get("recruitProject") != ctProject || q.Get("recruitType") != "1" {
				t.Error("wrong unit scope")
			}
			id, _ := strconv.Atoi(ctUnits[adapter].ID)
			write(envelope(map[string]any{"uniqueKey": id, "cnName": ctUnits[adapter].Name + "*"}))
			return
		}
		id, page := 0, 1
		switch adapter {
		case "ths":
			if strings.HasSuffix(r.URL.Path, "apply_detail") {
				id, _ = strconv.Atoi(q.Get("id"))
			} else {
				page, _ = strconv.Atoi(q.Get("page"))
				if q.Get("pageCount") != "50" || q.Get("type") != "school" || q.Get("applyRecruitmentSeriesIds") != "61" {
					t.Error("wrong project")
				}
			}
		case "cmbnt":
			if strings.HasSuffix(r.URL.Path, "officialSelect") {
				n, _ := strconv.ParseInt(q.Get("jobId"), 16, 32)
				id = int(n)
			} else {
				page, _ = strconv.Atoi(q.Get("pageIndex"))
				page++
				if q.Get("pageSize") != "50" || q.Get("recruitType") != "0" {
					t.Error("wrong graduate filter")
				}
			}
		case "netease_game":
			if q.Get("projectId") != "102" || q.Get("pageSize") != "50" {
				t.Error("wrong game project")
			}
			id, _ = strconv.Atoi(q.Get("positionIdList"))
			page, _ = strconv.Atoi(q.Get("currentPage"))
		case "leihuo":
			if q.Get("project_id") != "77" {
				t.Error("wrong leihuo project")
			}
			if strings.HasSuffix(r.URL.Path, "detail/show") {
				id, _ = strconv.Atoi(q.Get("job_id"))
			} else {
				page, _ = strconv.Atoi(q.Get("page_number"))
				if q.Get("page_size") != "50" {
					t.Error("wrong page size")
				}
			}
		default:
			if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Error("list must use published form query")
			}
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.PostForm.Get("recruitType") != "1" {
				t.Error("wrong campus filter")
			}
			if strings.HasSuffix(r.URL.Path, "/detail") {
				id, _ = strconv.Atoi(r.PostForm.Get("postId"))
			} else {
				page, _ = strconv.Atoi(r.PostForm.Get("rowIndex"))
				if r.PostForm.Get("org") != ctUnits[adapter].ID || r.PostForm.Get("recruitProject") != ctProject || r.PostForm.Get("rowSize") != "50" {
					t.Error("wrong unit/project")
				}
			}
		}
		if id > 0 {
			row := sectorRow(adapter, id)
			if adapter == "ctyun" || adapter == "ctcloud" {
				row = map[string]any{"name": row["PostName"], "OrgName": ctUnits[adapter].Name, "orgCode": "0/" + ctUnits[adapter].ID, "projectName": ctProjectName, "workConcet": "开发 Go 服务", "serviceCondition": "Go、Linux；Redis 加分", "EducationName": "本科"}
			}
			mutateSector(row, adapter, scenario)
			if adapter == "netease_game" {
				write(envelope(map[string]any{"total": 1, "pages": 1, "list": []any{row}}))
			} else {
				write(envelope(row))
			}
			return
		}
		total := 51
		if scenario == "capacity" {
			total = 501
		}
		if scenario == "empty" {
			total = 0
		}
		if scenario == "drift" && page == 2 {
			total++
		}
		rows := []any{}
		for n := (page-1)*50 + 1; n <= min(total, page*50); n++ {
			i := n
			if scenario == "duplicate" && page == 2 {
				i = 1
			}
			row := sectorRow(adapter, i)
			if scenario == "mixed_scope" || scenario == "unknown_kind" {
				mutateSector(row, adapter, scenario)
			}
			rows = append(rows, row)
		}
		if scenario == "truncated" && len(rows) > 0 {
			rows = rows[:len(rows)-1]
		}
		echo := page
		if scenario == "page_echo" {
			echo++
		}
		var data map[string]any
		countKey := "total"
		switch adapter {
		case "ths":
			data = map[string]any{"total": total, "current": echo, "size": 50, "pages": (total + 49) / 50, "apply_show_do_list": rows}
		case "cmbnt":
			data = map[string]any{"total": total, "pageIndex": echo - 1, "pageSize": 50, "data": rows}
		case "netease_game":
			data = map[string]any{"total": total, "pages": (total + 49) / 50, "list": rows, "lastPage": false}
		case "leihuo":
			countKey = "count_number"
			data = map[string]any{"count_number": total, "pages_count": (total + 49) / 50, "last_page": page >= max(1, (total+49)/50), "apply_job_list": rows}
		default:
			countKey = "rowCount"
			data = map[string]any{"rowCount": total, "rowIndex": echo, "rowSize": 50, "details": rows}
		}
		if scenario == "missing_total" {
			delete(data, countKey)
		}
		body := envelope(data)
		if scenario == "missing_code" {
			delete(body, "success")
			delete(body, "type")
			delete(body, "code")
			delete(body, "status")
		}
		write(body)
	}))
}

func TestSectorCampusFullPaginationAndOriginals(t *testing.T) {
	for _, adapter := range sectorAdapters {
		t.Run(adapter, func(t *testing.T) {
			srv := sectorFixture(t, adapter, "")
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(expandedSite(adapter).URL)
			jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
			client := &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}
			key := adapter + ":public-site"
			if adapter == "ctyun" || adapter == "ctcloud" {
				key = "chinatelecom:public-site"
			}
			a := PublicPlatform{Client: client, Allow: func(_ context.Context, k string, limit int) (bool, error) {
				if k != key || limit != 30 {
					t.Error("site pacing lost")
				}
				return true, nil
			}}
			preview, err := a.PreviewCampus(context.Background(), expandedSite(adapter).URL)
			if err != nil || preview.Total != 51 || preview.MinimumInterval != 1800 {
				t.Fatalf("preview %+v %v", preview, err)
			}
			s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(refs) != 51 {
				t.Fatalf("scope %d %v", len(refs), err)
			}
			for _, r := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range []PostingRef{refs[0], refs[50]} {
				res, err := a.FetchPosting(context.Background(), s, r)
				if err != nil || !strings.Contains(res.Text, "Redis 加分") || strings.Contains(res.Text, "<p>") {
					t.Fatalf("original %+v %v", res, err)
				}
				if adapter == "ths" && (r.JobType != "UNKNOWN" || strings.Contains(res.Text, "应届生全职")) {
					t.Fatal("conversion internship falsely promoted")
				}
				if adapter == "cmbnt" && strings.Contains(res.Text, "2100-") {
					t.Fatal("sentinel emitted as deadline")
				}
			}
			selected, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "Go 工程师 51"}})
			if err != nil || len(selected) != 1 || selected[0].ExternalID != sectorID(adapter, 51) {
				t.Fatal("last page lost before keyword filter")
			}
			if client.Jar != jar {
				t.Fatal("caller client mutated")
			}
		})
	}
}
func TestSectorCampusRejectsIncompleteAndMixedLists(t *testing.T) {
	for _, adapter := range sectorAdapters {
		scenarios := []string{"mixed_scope", "truncated", "drift", "duplicate", "capacity", "missing_total", "missing_code", "http_blocked", "redirect"}
		if adapter == "ths" || adapter == "cmbnt" || adapter == "ctyun" || adapter == "ctcloud" {
			scenarios = append(scenarios, "page_echo")
		}
		if adapter == "ths" || adapter == "netease_game" || adapter == "ctyun" || adapter == "ctcloud" {
			scenarios = append(scenarios, "closed")
		}
		if adapter == "cmbnt" || adapter == "leihuo" || adapter == "ctyun" || adapter == "ctcloud" {
			scenarios = append(scenarios, "unknown_kind")
		}
		for _, scenario := range scenarios {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := sectorFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				refs, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
				if err == nil || refs != nil {
					t.Fatalf("partial or wrong scope accepted %d %v", len(refs), err)
				}
			})
		}
	}
}
func TestSectorCampusRejectsWrongOrEmptyDetails(t *testing.T) {
	for _, adapter := range sectorAdapters {
		scenarios := []string{"detail_scope", "detail_id", "detail_title", "empty_requirements", "empty_duties"}
		if adapter == "ctyun" || adapter == "ctcloud" {
			scenarios = append(scenarios, "detail_project")
		}
		if adapter == "leihuo" {
			scenarios = append(scenarios, "wrong_target")
		}
		for _, scenario := range scenarios {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := sectorFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				s := d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}
				refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
				if err != nil {
					t.Fatal(err)
				}
				res, err := a.FetchPosting(context.Background(), s, refs[0])
				if err == nil || res.Status == "SUCCESS" {
					t.Fatal("unverifiable detail accepted")
				}
			})
		}
	}
}
func TestSectorCampusEmptyGraduateScopeAndRateLimit(t *testing.T) {
	for _, scenario := range []string{"empty", "paced"} {
		t.Run(scenario, func(t *testing.T) {
			srv := sectorFixture(t, "cmbnt", map[string]string{"empty": "empty"}[scenario])
			defer srv.Close()
			calls := 0
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}, Allow: func(context.Context, string, int) (bool, error) { calls++; return calls < 2, nil }}
			refs, err := a.Discover(context.Background(), d.Source{Adapter: "cmbnt", Tenant: "graduate"}, d.WatchTarget{})
			if scenario == "empty" {
				if err != nil || refs == nil || len(refs) != 0 {
					t.Fatal("valid empty scope failed")
				}
			} else {
				var f *FetchError
				if !AsFetchError(err, &f) || f.Category != "RATE_LIMIT" || refs != nil {
					t.Fatal("partial paced scan accepted")
				}
			}
		})
	}
}

func TestLeihuoPreservesExplicitFutureGraduateTarget(t *testing.T) {
	srv := sectorFixture(t, "leihuo", "future_target")
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	s := d.Source{Adapter: "leihuo", Tenant: "77"}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.FetchPosting(context.Background(), s, refs[0])
	if err != nil || !strings.Contains(res.Text, "官网招聘对象：27届及之后毕业") {
		t.Fatalf("target lost %+v %v", res, err)
	}
}
func TestSectorCampusStrictEntryAndScopeBeforeNetwork(t *testing.T) {
	for _, adapter := range sectorAdapters {
		t.Run(adapter, func(t *testing.T) {
			site := expandedSite(adapter)
			if RecognizeCampusURL(site.URL) != nil {
				t.Fatal("preset rejected")
			}
			u, _ := url.Parse(site.URL)
			q := u.Query()
			q.Set("inviteCode", "private")
			u.RawQuery = q.Encode()
			if RecognizeCampusURL(u.String()) == nil {
				t.Fatal("invitation link accepted")
			}
			calls := 0
			a := PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected") })}}
			s := d.Source{Adapter: adapter, Tenant: "intern"}
			if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil || calls != 0 {
				t.Fatal("wrong tenant reached network")
			}
			s.Tenant = moreTenant(adapter)
			if _, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil || calls != 0 {
				t.Fatal("unsupported direction reached network")
			}
		})
	}
	// Decode only our emitted public route. The official page will reconstruct
	// unit and project filters from these arrays; it has no direct job permalink.
	u, _ := url.Parse(ctJobURL("ctyun", "1", "Go 工程师"))
	q, _ := url.ParseQuery(strings.SplitN(u.Fragment, "?", 2)[1])
	b, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(q.Get("data"), "~2F", "/"))
	var route struct {
		Title   string            `json:"postName"`
		Filters map[string]string `json:"filters"`
	}
	if err != nil || json.Unmarshal(b, &route) != nil || route.Title != "Go 工程师" || route.Filters["org"] != "[581854]" || route.Filters["recruitProject"] != "[101101]" {
		t.Fatal("original search scope lost")
	}
}
