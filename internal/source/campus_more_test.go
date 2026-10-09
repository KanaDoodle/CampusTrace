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

var moreAdapters = []string{"h3c", "yusys", "cksic", "whxmc", "neusoft", "mthreads", "nexchip", "lenovo", "midea", "byd", "hikvision", "qihoo360", "sany", "inovance", "vivo", "honor", "sgm", "hundsun", "yuewen", "csc_securities", "guosen_securities", "galaxy_securities", "cicc_securities"}

func moreID(adapter string, n int) string {
	switch adapter {
	case "midea", "hikvision":
		return fmt.Sprintf("%032x", n)
	case "honor":
		return fmt.Sprintf("%024x", n)
	case "lenovo", "byd":
		return strconv.Itoa(n)
	default:
		return fmt.Sprintf("00000000-0000-0000-0000-%012d", n)
	}
}
func moreNumber(adapter, id string) int {
	if adapter == "midea" || adapter == "hikvision" || adapter == "honor" {
		n, _ := strconv.ParseInt(id, 16, 32)
		return int(n)
	}
	if strings.Contains(id, "-") {
		id = id[strings.LastIndex(id, "-")+1:]
	}
	n, _ := strconv.Atoi(id)
	return n
}
func moreRow(adapter string, n int) map[string]any {
	id, name := moreID(adapter, n), fmt.Sprintf("Go 工程师 %d", n)
	switch adapter {
	case "lenovo":
		return map[string]any{"id": n, "jobName": name, "projectType": 1, "publishFlag": 1, "activateFlag": 1, "workPlace": "1,2", "jobDuties": "<p>开发 Go 服务</p>", "jobRequirement": "Go、Linux；Redis 加分"}
	case "midea":
		return map[string]any{"positionId": id, "projectRuleId": mideaProject, "projectType": "1", "employementCategory": 1, "projectPositionName": name, "publishStatus": 1, "workplaceDtoList": []any{map[string]any{"workPlaceName": "北京"}}, "projectPositionDto": map[string]any{"positionName": name, "jobResponsibility": "<p>开发 Go 服务</p>", "jobRequirement": "Go、Linux；Redis 加分"}}
	case "byd":
		return map[string]any{"id": id, "jobName": name, "batch": 2027, "campusNature": "008501", "workPlace": "深圳", "positionInfoList": []any{map[string]any{"abroad": "00112", "division": "研发部", "researchDirection": "后端", "workPlace": "深圳", "jobDuty": "<p>开发 Go 服务</p>", "jobRequirements": "Go、Linux；Redis 加分"}}}
	case "hikvision":
		return map[string]any{"id": id, "batchId": hikvisionProject, "batchName": "【2027校园招聘】", "postAdName": name, "batchPositionName": "【2027校园招聘】" + name, "jobNature": "校招应届生", "yn": 1, "status": 1, "mergeType": 0, "workPlace": "杭州/北京", "postContent": "<p>开发 Go 服务</p>", "postRequire": "Go、Linux；Redis 加分"}
	case "honor":
		return map[string]any{"postId": id, "postName": name, "recruitType": 1, "projectId": 101801, "projectName": "2027届应届本硕", "workTypeStr": "全职", "workPlaceStr": "深圳", "workContent": "<p>开发 Go 服务</p>", "serviceCondition": "Go、Linux；Redis 加分", "endDate": "2026-12-31", "canDelivery": false}
	default:
		return map[string]any{"Id": id, "JobAdId": 9000 + n, "JobAdName": name, "CategoryId": "2", "Status": 1, "LocNames": []string{"北京"}, "Duty": "<p>开发 Go 服务</p>", "Require": "Go、Linux；Redis 加分"}
	}
}
func mutateMore(row map[string]any, adapter, scenario string) {
	if scenario == "mixed_scope" || scenario == "detail_scope" {
		switch adapter {
		case "lenovo":
			row["projectType"] = 3
		case "midea":
			row["projectRuleId"] = "intern-project"
		case "byd":
			row["campusNature"] = "008502"
		case "hikvision":
			row["jobNature"] = "实习生"
		case "honor":
			row["projectId"] = 101800
		default:
			row["CategoryId"] = "3"
		}
	}
	if scenario == "inactive" {
		switch adapter {
		case "lenovo":
			row["publishFlag"] = 0
		case "midea":
			row["employementCategory"] = 2
		case "byd":
			row["batch"] = 2026
		case "hikvision":
			row["yn"] = 0
		case "honor":
			row["workTypeStr"] = "实习"
		default:
			row["Status"] = 0
		}
	}
	if scenario == "empty_requirements" {
		switch adapter {
		case "lenovo":
			row["jobRequirement"] = "<p> </p>"
		case "midea":
			row["projectPositionDto"].(map[string]any)["jobRequirement"] = ""
		case "byd":
			row["positionInfoList"].([]any)[0].(map[string]any)["jobRequirements"] = ""
		case "hikvision":
			row["postRequire"] = ""
		case "honor":
			row["serviceCondition"] = ""
		default:
			row["Require"] = ""
		}
	}
}

// Synthetic records only. Official public wire formats are verified separately
// by the opt-in live test; CI never depends on network or candidate credentials.
func moreFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != expandedSite(adapter).URL {
			t.Error("session leaked or official referer missing")
		}
		if scenario == "http_blocked" {
			w.WriteHeader(403)
			return
		}
		if scenario == "redirect" {
			http.Redirect(w, r, "https://example.org/private", 302)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		envelope := func(data any) map[string]any {
			switch adapter {
			case "lenovo":
				return map[string]any{"code": 0, "result": data}
			case "midea":
				return map[string]any{"code": "0", "data": data}
			case "byd":
				return map[string]any{"code": 0, "oK": true, "data": data}
			case "hikvision":
				return map[string]any{"status": 200, "success": true, "data": data}
			case "honor":
				return map[string]any{"state": "200", "type": "success", "data": data}
			default:
				return map[string]any{"Code": 200, "Data": data}
			}
		}
		write := func(v map[string]any) {
			if scenario == "missing_code" {
				for _, key := range []string{"code", "Code", "state", "status"} {
					delete(v, key)
				}
			}
			json.NewEncoder(w).Encode(v)
		}
		path := r.URL.Path
		if adapter == "lenovo" && path == "/gateway/proj/status" {
			row := map[string]any{"projectType": 1, "activateFlag": 1}
			if scenario == "changed_project" {
				row["activateFlag"] = 0
			}
			rows := []any{row}
			if scenario == "duplicate_project" {
				rows = append(rows, row)
			}
			write(envelope(rows))
			return
		}
		if adapter == "lenovo" && path == "/gateway/sysDict/all" {
			write(envelope([]any{map[string]any{"dictCode": "projectType", "children": []any{map[string]any{"dictValue": 1, "dictName": "应届生招聘"}}}, map[string]any{"dictCode": "city_portal", "children": []any{map[string]any{"dictValue": 1, "dictName": "北京"}, map[string]any{"dictValue": 2, "dictName": "上海"}}}}))
			return
		}
		if adapter == "midea" && strings.HasSuffix(path, "/project/list") {
			row := map[string]any{"projectRuleId": mideaProject, "projectRuleName": "2027届美的星校园招聘", "projectType": "1", "numberOfSessions": "2027", "employementCategory": 1, "status": 1}
			if scenario == "changed_project" {
				row["numberOfSessions"] = "2028"
			}
			rows := []any{row}
			if scenario == "duplicate_project" {
				rows = append(rows, row)
			}
			write(envelope(rows))
			return
		}
		if adapter == "byd" && strings.HasSuffix(path, "/postEntryConfig/list") {
			row := map[string]any{"postEntryName": "应届生", "schoolTopic": bydProject, "batch": "2027", "campusNature": "008501", "abroad": "00112", "degree": "", "status": "00111", "content": "2027年应届毕业生"}
			if scenario == "changed_project" {
				row["schoolTopic"] = "wrong"
			}
			rows := []any{row}
			if scenario == "duplicate_project" {
				rows = append(rows, row)
			}
			write(envelope(rows))
			return
		}
		if adapter == "hikvision" && strings.HasSuffix(path, "/findNotZxfBatchAll") {
			row := map[string]any{"id": hikvisionProject, "batchName": "【2027校园招聘】", "status": 1, "yn": 1, "remark": "2027年应届毕业生"}
			if scenario == "changed_project" {
				row["status"] = 0
			}
			rows := []any{row}
			if scenario == "duplicate_project" {
				rows = append(rows, row)
			}
			write(envelope(rows))
			return
		}
		values := map[string]any{}
		if r.Method == "POST" {
			if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
				if err := json.NewDecoder(r.Body).Decode(&values); err != nil {
					t.Error(err)
				}
			} else {
				r.ParseForm()
				for k, v := range r.PostForm {
					values[k] = v[0]
				}
			}
		}
		integer := func(key string) int {
			switch v := values[key].(type) {
			case float64:
				return int(v)
			case string:
				n, _ := strconv.Atoi(v)
				return n
			}
			return 0
		}
		id := ""
		switch adapter {
		case "lenovo":
			id = r.URL.Query().Get("jobId")
		case "midea":
			if strings.HasSuffix(path, "/details") {
				id, _ = values["positionId"].(string)
			}
		case "byd":
			if strings.HasSuffix(path, "/queryPosition") {
				id = r.URL.Query().Get("id")
			}
		case "hikvision":
			if strings.HasSuffix(path, "/findId") || strings.HasSuffix(path, "/findIdMerge") {
				id = r.URL.Query().Get("id")
			}
		case "honor":
			if strings.Contains(path, "/listPositionDetail/") {
				id, _ = values["postId"].(string)
			}
		default:
			if strings.HasSuffix(path, "/GetJobAdInfo") {
				id = r.URL.Query().Get("jobAdId")
			}
		}
		if id != "" {
			n := moreNumber(adapter, id)
			if scenario == "wrong_id" {
				n++
			}
			row := moreRow(adapter, n)
			mutateMore(row, adapter, scenario)
			if adapter == "hikvision" && strings.HasPrefix(scenario, "merged") {
				row["mergeType"] = 2
				row["postContent"] = nil
				row["postRequire"] = nil
				if strings.HasSuffix(path, "/findIdMerge") {
					child := moreRow(adapter, n+100)
					child["postAdName"] = row["postAdName"]
					child["mergeType"] = 1
					child["mergeRootId"] = id
					child["needDeptName"] = "研发平台部"
					child["techDire"] = "服务端"
					if scenario == "merged_wrong_root" {
						child["mergeRootId"] = moreID(adapter, 999)
					}
					if scenario == "merged_empty_requirements" {
						child["postRequire"] = ""
					}
					if scenario == "merged_scope" {
						child["batchId"] = "intern"
					}
					children := []any{child}
					if scenario == "merged_duplicate" {
						children = append(children, child)
					}
					write(envelope(children))
					return
				}
			}
			if adapter == "lenovo" {
				write(envelope(map[string]any{"rows": []any{row}, "total": 1}))
				return
			}
			write(envelope(row))
			return
		}
		page, size := 1, 50
		switch adapter {
		case "lenovo":
			page, _ = strconv.Atoi(r.URL.Query().Get("pageNum"))
			if r.URL.Query().Get("projectType") != "1" || r.URL.Query().Get("pageSize") != "50" {
				t.Error("lenovo scope/page")
			}
		case "midea":
			page, size = integer("pageIndex"), 20
			if values["projectRuleId"] != mideaProject || integer("pageSize") != 20 {
				t.Error("midea scope/page")
			}
		case "byd":
			size = MaxPostings
			if values["topicCode"] != bydProject || values["abroad"] != "00112" || values["campusNature"] != "008501" || integer("pageSize") != MaxPostings || integer("pageIndex") != 1 {
				t.Error("byd scope/page")
			}
		case "hikvision":
			page = integer("pageNum")
			if values["batchId"] != hikvisionProject || values["jobNature"] != "应届生" || values["notZxfFlag"] != "notZxfFlag" {
				t.Error("hikvision scope/page")
			}
		case "honor":
			page, size = integer("currentPage"), 15
			if values["projectCode"] != honorProject || values["recruitType"] != "1" || integer("pageSize") != 15 {
				t.Error("honor scope/page")
			}
		default:
			page = integer("PageIndex") + 1
			if integer("category") != 2 || integer("PageSize") != 50 {
				t.Error("beisen zero-based scope/page")
			}
		}
		total := size + 1
		if adapter == "byd" {
			total = 2
		}
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
		for n := (page-1)*size + 1; n <= min(page*size, total); n++ {
			index := n
			if scenario == "duplicate" && (page > 1 || adapter == "byd" && n == 2) {
				index = 1
			}
			row := moreRow(adapter, index)
			if scenario != "detail_scope" {
				mutateMore(row, adapter, scenario)
			}
			if strings.HasPrefix(scenario, "merged") && adapter == "hikvision" {
				row["mergeType"] = 2
			}
			if scenario == "unknown_city" && adapter == "lenovo" {
				row["workPlace"] = "999"
			}
			rows = append(rows, row)
		}
		if scenario == "truncated" && len(rows) > 0 {
			rows = rows[:len(rows)-1]
		}
		echoed := page
		if scenario == "page_echo" {
			echoed++
		}
		var body map[string]any
		switch adapter {
		case "lenovo":
			data := map[string]any{"rows": rows, "total": total}
			if scenario == "missing_total" {
				delete(data, "total")
			}
			body = envelope(data)
		case "midea":
			data := map[string]any{"data": rows, "total": total, "info": map[string]any{"pageIndex": echoed, "pageSize": size, "totalPage": (total + size - 1) / size}}
			if scenario == "missing_total" {
				delete(data, "total")
			}
			body = envelope(data)
		case "byd":
			body = envelope(rows)
			pg := map[string]any{"pageIndex": echoed, "pageSize": size, "totalCount": total, "totalPage": (total + size - 1) / size}
			if scenario == "missing_total" {
				delete(pg, "totalCount")
			}
			body["page"] = pg
		case "hikvision":
			data := map[string]any{"list": rows, "total": total, "pageNum": echoed, "pageSize": size}
			if scenario == "missing_total" {
				delete(data, "total")
			}
			body = envelope(data)
		case "honor":
			data := map[string]any{"pageData": rows, "dataCount": total, "currentPage": echoed, "pageSize": size, "totalPage": (total + size - 1) / size}
			if scenario == "missing_total" {
				delete(data, "dataCount")
			}
			body = envelope(map[string]any{"pageForm": data})
		default:
			body = envelope(rows)
			body["Count"] = total
			body["Total"] = 0
			if scenario == "missing_total" {
				delete(body, "Count")
			}
		}
		write(body)
	}))
}
func TestMoreCampusFullScopeAndIndependentDetails(t *testing.T) {
	for _, adapter := range moreAdapters {
		t.Run(adapter, func(t *testing.T) {
			srv := moreFixture(t, adapter, "")
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(expandedSite(adapter).URL)
			jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
			original := &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}
			paced := 0
			a := PublicPlatform{Client: original, Allow: func(_ context.Context, key string, limit int) (bool, error) {
				if key != adapter+":public-site" || limit != 30 {
					t.Error("company pacing lost")
				}
				paced++
				return true, nil
			}}
			ctx := context.Background()
			site := expandedSite(adapter)
			preview, err := a.PreviewCampus(ctx, site.URL)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Name != site.Company+" · 校招" || len(preview.Name) > 160 {
				t.Fatalf("preview source name cannot be saved: %q", preview.Name)
			}
			s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter), RateLimit: 30}
			refs, err := a.Discover(ctx, s, d.WatchTarget{})
			if err != nil || len(refs) != preview.Total || preview.MinimumInterval != 1800 || len(preview.Samples) != min(5, len(refs)) {
				t.Fatalf("full scope %d / %+v %v", len(refs), preview, err)
			}
			for _, ref := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
				result, err := a.FetchPosting(ctx, s, ref)
				if err != nil || result.Status != "SUCCESS" || !strings.Contains(result.Text, "Redis 加分") || strings.Contains(result.Text, "<p>") {
					t.Fatalf("detail %+v %v", result, err)
				}
				if isSecuritiesBeisen(adapter) && (ref.JobType != "UNKNOWN" || strings.Contains(result.Text, "应届生全职") || !strings.Contains(result.Text, "用工形式")) {
					t.Fatal("campus category incorrectly treated as full-time employment")
				}
			}
			selected, err := a.Discover(ctx, s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "Go 工程师 1"}})
			if err != nil || len(selected) == 0 || len(selected) >= len(refs) {
				t.Fatalf("post-scan keyword %d %v", len(selected), err)
			}
			if original.Jar != jar || paced == 0 {
				t.Fatal("client mutated or pacing skipped")
			}
		})
	}
}
func TestMoreCampusRejectsIncompleteAndWrongScopes(t *testing.T) {
	for _, adapter := range moreAdapters {
		scenarios := []string{"mixed_scope", "inactive", "truncated", "duplicate", "capacity", "missing_code", "missing_total", "http_blocked", "redirect"}
		if adapter != "byd" {
			scenarios = append(scenarios, "drift")
		}
		if adapter == "midea" || adapter == "hikvision" || adapter == "honor" || adapter == "byd" {
			scenarios = append(scenarios, "page_echo")
		}
		if adapter == "lenovo" || adapter == "midea" || adapter == "byd" || adapter == "hikvision" {
			scenarios = append(scenarios, "changed_project", "duplicate_project")
		}
		if adapter == "lenovo" {
			scenarios = append(scenarios, "unknown_city")
		}
		for _, scenario := range scenarios {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := moreFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				refs, err := a.Discover(context.Background(), d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
				if err == nil || len(refs) != 0 {
					t.Fatalf("partial or invalid success %d %v", len(refs), err)
				}
			})
		}
		for _, scenario := range []string{"wrong_id", "detail_scope", "empty_requirements"} {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := moreFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}
				refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
				if err != nil {
					t.Fatal(err)
				}
				result, err := a.FetchPosting(context.Background(), s, refs[0])
				if err == nil || result.Status == "SUCCESS" {
					t.Fatal("invalid detail accepted")
				}
			})
		}
	}
}
func TestMoreCampusEmptyAndDeferredScans(t *testing.T) {
	for _, adapter := range moreAdapters {
		t.Run(adapter, func(t *testing.T) {
			s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}
			srv := moreFixture(t, adapter, "empty")
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			v, err := a.PreviewCampus(context.Background(), expandedSite(adapter).URL)
			if err != nil || v.Total != 0 {
				t.Fatalf("empty %+v %v", v, err)
			}
			full := moreFixture(t, adapter, "")
			defer full.Close()
			stop := map[string]int{"lenovo": 4, "midea": 3, "hikvision": 3, "byd": 2}[adapter]
			if stop == 0 {
				stop = 2
			}
			calls := 0
			a = PublicPlatform{Client: &http.Client{Transport: rewriteTransport{full.URL}}, Allow: func(context.Context, string, int) (bool, error) { calls++; return calls < stop, nil }}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			var fetchErr *FetchError
			if len(refs) != 0 || !AsFetchError(err, &fetchErr) || fetchErr.Category != "RATE_LIMIT" || !fetchErr.Retryable {
				t.Fatalf("partial deferred scan %d %v", len(refs), err)
			}
		})
	}
}
func TestHikvisionMergedDepartmentsAreChecked(t *testing.T) {
	for _, scenario := range []string{"merged", "merged_wrong_root", "merged_duplicate", "merged_empty_requirements", "merged_scope"} {
		t.Run(scenario, func(t *testing.T) {
			srv := moreFixture(t, "hikvision", scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{ID: d.ID(), Adapter: "hikvision", Tenant: hikvisionProject}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.FetchPosting(context.Background(), s, refs[0])
			if scenario == "merged" {
				if err != nil || !strings.Contains(result.Text, "研发平台部") || !strings.Contains(result.Text, "服务端") || !strings.Contains(result.Text, "Redis 加分") {
					t.Fatalf("missing child evidence %+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("wrong child accepted")
			}
		})
	}
}
func TestMoreCampusDirectoryAndStrictEntry(t *testing.T) {
	entries := CampusDirectory()
	if len(entries) != 108 {
		t.Fatalf("directory %d", len(entries))
	}
	readyCount := 0
	companies := map[string]bool{}
	for _, entry := range entries {
		if entry.Company == "" || entry.Category == "" || entry.Note == "" || companies[entry.Company] {
			t.Fatalf("invalid entry %+v", entry)
		}
		companies[entry.Company] = true
		if entry.AutoImport {
			readyCount++
			if !d.IsCampusSource(entry.Adapter) || RecognizeCampusURL(entry.URL) != nil {
				t.Fatal("implemented entry missing scope")
			}
		} else if entry.Adapter != "" || RecognizeCampusURL(entry.URL) == nil {
			t.Fatal("manual entry promoted to import")
		}
	}
	if readyCount != 64 {
		t.Fatalf("automatic sources %d", readyCount)
	}
	for _, adapter := range moreAdapters {
		site := expandedSite(adapter)
		u, _ := url.Parse(site.URL)
		q := u.Query()
		q.Set("shareId", "private-referral")
		u.RawQuery = q.Encode()
		if RecognizeCampusURL(u.String()) == nil {
			t.Fatal("private filter accepted")
		}
		calls := 0
		a := PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected network") })}}
		s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: "intern"}
		if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil || calls != 0 {
			t.Fatal("wrong scope reached network")
		}
		s.Tenant = moreTenant(adapter)
		if _, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil || calls != 0 {
			t.Fatal("unsupported direction reached network")
		}
	}
}

func TestBeisenDetailTitleIsBoundToListing(t *testing.T) {
	for adapter := range beisenCompanies {
		t.Run(adapter, func(t *testing.T) {
			srv := moreFixture(t, adapter, "")
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: "campus"}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil {
				t.Fatal(err)
			}
			refs[0].Title = "另一个岗位"
			result, err := a.FetchPosting(context.Background(), s, refs[0])
			if err == nil || result.Status == "SUCCESS" {
				t.Fatal("wrong title accepted")
			}
		})
	}
}
