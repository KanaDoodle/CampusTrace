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
	"regexp"
	"strconv"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

var nextAdapters = []string{"mihoyo", "pingan_tech", "pingan_oneconnect", "pingan_wallet", "cmcloud", "cmiot", "cmhome"}

func nextID(adapter string, n int) string {
	if adapter == "mihoyo" {
		return strconv.Itoa(n)
	}
	if _, ok := pinganUnits[adapter]; ok {
		return fmt.Sprintf("%032x", n)
	}
	return fmt.Sprintf("00000000-0000-0000-0000-%012d", n)
}
func nextRow(adapter string, n int) map[string]any {
	id, title := nextID(adapter, n), fmt.Sprintf("Go 工程师 %d", n)
	if adapter == "mihoyo" {
		return map[string]any{"id": id, "title": title, "addressDetailList": []any{map[string]any{"addressDetail": "上海"}}, "jobNatureId": 1, "jobNature": "全职", "projectName": mihoyoProjectName, "objectId": "19", "objectName": mihoyoTarget, "channelDetailIds": []int{1, 2}, "projectId": 13, "hireType": 1, "status": 1, "description": "<p>开发 Go 服务</p>", "jobRequire": "Go、Linux", "addition": "Redis 加分", "deliveryInstructions": "申请前核对原文"}
	}
	if u, ok := pinganUnits[adapter]; ok {
		return map[string]any{"idPosition": id, "positionId": id, "positionName": title, "businessUnitId": u.ID, "businessUnitName": u.Name, "positionType": "全职", "positionTypes": "1", "publishStatus": "P", "workCity": "上海市,深圳市", "duty": "<p>开发 Go 服务</p>", "qualification": "Go、Linux；Redis 加分", "education": "本科", "deptShowName": "研发部", "checkResumeRepeat": "N", "deliverDate": "private candidate state"}
	}
	u := mobileUnits[adapter]
	return map[string]any{"id": id, "name": title, "company": u.Company, "companyShortName": u.Name, "type": "1", "workType": "02", "city": "苏州", "description": "Go、Linux；Redis 加分", "dutyCondition": ""}
}
func mutateNext(row map[string]any, adapter, scenario string) {
	_, pa := pinganUnits[adapter]
	key := func(mh, pg, cm string) string {
		if adapter == "mihoyo" {
			return mh
		}
		if pa {
			return pg
		}
		return cm
	}
	switch scenario {
	case "mixed_scope", "detail_scope":
		row[key("projectName", "businessUnitId", "company")] = "other scope"
	case "unknown_kind":
		delete(row, key("jobNatureId", "positionType", "workType"))
	case "detail_id":
		row[key("id", "positionId", "id")] = nextID(adapter, 999)
	case "detail_title":
		row[key("title", "positionName", "name")] = "其他岗位"
	case "empty_original":
		row[key("description", "duty", "description")] = "<p> </p>"
		row[key("jobRequire", "qualification", "dutyCondition")] = ""
	case "empty_requirements":
		row[key("jobRequire", "qualification", "dutyCondition")] = "<p> </p>"
	case "wrong_target":
		row[key("objectName", "positionTypes", "type")] = "intern"
	}
}
func nextFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != expandedSite(adapter).URL {
			t.Error("lost anonymous query scope")
		}
		if scenario == "http_blocked" {
			w.WriteHeader(403)
			return
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
		_, pa := pinganUnits[adapter]
		_, cm := mobileUnits[adapter]
		data := body
		if cm {
			header, _ := body["header"].(map[string]any)
			digest, _ := header["digest"].(string)
			parts := strings.Split(digest, ";")
			if len(parts) != 2 || header["version"] != "1.0" || header["timestamp"] == nil || !regexp.MustCompile(`^\d{23}$`).MatchString(fmt.Sprint(header["conversationId"])) {
				t.Error("invalid public nonce header")
			}
			if len(parts) == 2 {
				h, he := base64.StdEncoding.DecodeString(parts[0])
				c, ce := base64.StdEncoding.DecodeString(parts[1])
				if he != nil || ce != nil || len(h) != 32 || len(c) != 256 {
					t.Error("invalid public digest")
				}
			}
			data, _ = body["data"].(map[string]any)
			if body["serviceName"] != strings.TrimSuffix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], ".do") {
				t.Error("unexpected public service")
			}
		}
		total := 51
		if cm {
			total = 21
		}
		if scenario == "capacity" {
			total = 501
		}
		if scenario == "empty" {
			total = 0
		}
		write := func(v any) {
			media := "application/json"
			if cm {
				media = "text/plain;charset=UTF-8"
			}
			if scenario == "html" {
				media = "text/html"
			}
			w.Header().Set("Content-Type", media)
			if scenario == "wrapped" {
				b, _ := json.Marshal(v)
				v = string(b)
			}
			if err := json.NewEncoder(w).Encode(v); err != nil {
				t.Error(err)
			}
		}
		wrap := func(data any) map[string]any {
			if adapter == "mihoyo" {
				return map[string]any{"code": 0, "success": true, "data": data}
			}
			if pa {
				return map[string]any{"responseCode": "10001", "data": data}
			}
			return map[string]any{"code": "0000", "data": data}
		}
		path := r.URL.Path
		if strings.HasSuffix(path, "/project_count/list") {
			if fmt.Sprint(data["hireType"]) != "1" || fmt.Sprint(data["channelDetailIds"]) != "[1]" {
				t.Error("incorrect campus channel")
			}
			ids := []int{13}
			if scenario == "wrong_catalog" {
				ids = []int{4}
			}
			write(wrap([]any{map[string]any{"projectIdList": ids, "projectName": "应届生投递", "count": total}, map[string]any{"projectIdList": []int{4}, "projectName": "实习生投递", "count": 17}}))
			return
		}
		if strings.HasSuffix(path, "/selectGroupOfficial") {
			if fmt.Sprint(data["websiteType"]) != "3" {
				t.Error("wrong website category")
			}
			group := pinganGroup
			if scenario == "wrong_catalog" {
				group = "other"
			}
			write(wrap(group))
			return
		}
		if strings.HasSuffix(path, "/queryCityCompanyCategory") {
			if data["wecruitId"] != pinganGroup || data["positionType"] != "1" {
				t.Error("wrong graduate category")
			}
			u := pinganUnits[adapter]
			write(wrap(map[string]any{"campusCompanyMap": map[string]any{"data": map[string]any{"科技创新业务": []any{map[string]any{"businessUnitId": u.ID, "companyName": u.Name}}}}}))
			return
		}
		if strings.HasSuffix(path, "/getCompanyList.do") {
			if fmt.Sprint(data["newJob"]) != "1" {
				t.Error("wrong company catalog")
			}
			u := mobileUnits[adapter]
			name := u.Name
			if scenario == "wrong_catalog" {
				name = "other"
			}
			write(wrap(map[string]any{"companyList": []any{map[string]any{"companyId": u.ID, "shortName": name, "type": 3}}}))
			return
		}
		detail := strings.HasSuffix(path, "/info") || strings.HasSuffix(path, "/queryPositionDetail")
		if detail {
			raw := data["id"]
			if pa {
				raw = data["positionId"]
				if data["wecruitId"] != pinganGroup || data["wecruitPlatform"] != true {
					t.Error("wrong public group")
				}
			}
			id := 0
			for n := 1; n <= 51; n++ {
				if fmt.Sprint(raw) == nextID(adapter, n) {
					id = n
					break
				}
			}
			if id == 0 {
				t.Error("bad detail id")
				w.WriteHeader(400)
				return
			}
			row := nextRow(adapter, id)
			mutateNext(row, adapter, scenario)
			if pa {
				row["positionType"] = ""
				write(wrap(map[string]any{"position": row, "description": map[string]any{"candidate": "private state"}}))
			} else {
				write(wrap(row))
			}
			return
		}
		page, size := 1, 50
		if pa {
			page = int(data["PageNum"].(float64))
			if data["businessUnitId"] != pinganUnits[adapter].ID || data["positionType"] != "1" || data["wecruitId"] != pinganGroup || data["wecruitPlatform"] != true {
				t.Error("mixed graduate/unit query")
			}
		} else {
			page = int(data["pageNo"].(float64))
			if cm {
				size = 20
				if data["companyId"] != mobileUnits[adapter].ID || fmt.Sprint(data["type"]) != "1" {
					t.Error("mixed campus/unit query")
				}
			} else {
				if fmt.Sprint(data["projectIds"]) != "[13]" || fmt.Sprint(data["hireType"]) != "1" || fmt.Sprint(data["channelDetailIds"]) != "[1]" {
					t.Error("mixed project/channel query")
				}
			}
		}
		if int(data["pageSize"].(float64)) != size {
			t.Error("wrong page size")
		}
		rows := []any{}
		for n := (page-1)*size + 1; n <= min(page*size, total); n++ {
			id := n
			if scenario == "duplicate" && page == 2 {
				id = 1
			}
			row := nextRow(adapter, id)
			if page == 2 || scenario == "mixed_scope" || scenario == "unknown_kind" {
				mutateNext(row, adapter, scenario)
			}
			rows = append(rows, row)
		}
		if scenario == "short_page" && len(rows) > 0 {
			rows = rows[:len(rows)-1]
		}
		echoed := page
		if scenario == "page_mismatch" {
			echoed = 1
		}
		actualTotal := total
		if scenario == "total_drift" && page == 2 {
			actualTotal++
		}
		response := map[string]any{}
		totalKey := "total"
		if pa {
			totalKey = "totalCount"
			response = map[string]any{"list": rows, "pageNo": echoed, "pageSize": size, "totalCount": actualTotal, "totalPage": (actualTotal + size - 1) / size}
		} else if cm {
			response = map[string]any{"jobList": rows, "total": actualTotal}
		} else {
			response = map[string]any{"list": rows, "pageNo": echoed, "pageSize": size, "total": actualTotal}
		}
		if scenario == "missing_total" {
			delete(response, totalKey)
		}
		env := wrap(response)
		if scenario == "missing_code" {
			delete(env, "code")
			delete(env, "responseCode")
		}
		write(env)
	}))
}
func nextClient(t *testing.T, srv *httptest.Server, adapter string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(expandedSite(adapter).URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
	if adapter == "mihoyo" {
		u, _ = url.Parse(mihoyoAPI)
		jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
	}
	return &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}
}
func TestNextCampusCompleteScopeAndOriginals(t *testing.T) {
	for _, adapter := range nextAdapters {
		t.Run(adapter, func(t *testing.T) {
			srv := nextFixture(t, adapter, "")
			defer srv.Close()
			key := adapter + ":public-site"
			if _, ok := pinganUnits[adapter]; ok {
				key = "pingan:public-site"
			}
			if _, ok := mobileUnits[adapter]; ok {
				key = "chinamobile:public-site"
			}
			a := PublicPlatform{Client: nextClient(t, srv, adapter), Allow: func(_ context.Context, k string, n int) (bool, error) {
				if k != key || n != 30 {
					t.Error("site-wide pacing lost")
				}
				return true, nil
			}}
			expected := 51
			if _, ok := mobileUnits[adapter]; ok {
				expected = 21
			}
			preview, err := a.PreviewCampus(context.Background(), expandedSite(adapter).URL)
			if err != nil || preview.Total != expected || preview.MinimumInterval != 1800 {
				t.Fatalf("preview %+v %v", preview, err)
			}
			s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(refs) != expected {
				t.Fatalf("scope %d %v", len(refs), err)
			}
			for _, r := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range []PostingRef{refs[0], refs[len(refs)-1]} {
				res, err := a.FetchPosting(context.Background(), s, r)
				if err != nil || !strings.Contains(res.Text, "Redis 加分") || strings.Contains(res.Text, "<p>") || strings.Contains(res.Text, "private") {
					t.Fatalf("original %+v %v", res, err)
				}
			}
			selected, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: fmt.Sprintf("Go 工程师 %d", expected)}})
			if err != nil || len(selected) != 1 || selected[0].ExternalID != nextID(adapter, expected) {
				t.Fatalf("keyword %v %v", selected, err)
			}
		})
	}
}
func TestNextCampusRejectsIncompleteOrMixedScopes(t *testing.T) {
	for _, adapter := range nextAdapters {
		for _, scenario := range []string{"wrong_catalog", "mixed_scope", "unknown_kind", "missing_total", "missing_code", "duplicate", "short_page", "total_drift", "capacity", "http_blocked", "html", "redirect"} {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := nextFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: nextClient(t, srv, adapter)}
				refs, err := a.Discover(context.Background(), d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "no matches"}})
				if err == nil || len(refs) != 0 {
					t.Fatalf("partial success %d %v", len(refs), err)
				}
			})
		}
	}
}
func TestNextCampusOriginalIdentityAndEvidence(t *testing.T) {
	for _, adapter := range nextAdapters {
		scenarios := []string{"detail_scope", "detail_id", "detail_title", "empty_original", "wrong_target"}
		if _, ok := mobileUnits[adapter]; !ok {
			scenarios = append(scenarios, "empty_requirements")
		}
		for _, scenario := range scenarios {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := nextFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: nextClient(t, srv, adapter)}
				row := nextRow(adapter, 1)
				b, _ := json.Marshal(row)
				var ref PostingRef
				if adapter == "mihoyo" {
					var j mihoyoJob
					_ = json.Unmarshal(b, &j)
					ref, _ = mihoyoRef(j)
				} else if _, ok := pinganUnits[adapter]; ok {
					var j pinganJob
					_ = json.Unmarshal(b, &j)
					ref, _ = pinganRef(adapter, j)
				} else {
					var j mobileJob
					_ = json.Unmarshal(b, &j)
					ref, _ = mobileRef(adapter, j)
				}
				// Mobile originals are bound to complete current pages. Mutate their first
				// row too so an invalid original cannot hide behind a valid requested ID.
				if _, ok := mobileUnits[adapter]; ok {
					srv.Close()
					srv = nextMobileDetailFixture(t, adapter, scenario)
					defer srv.Close()
					a.Client = nextClient(t, srv, adapter)
				}
				res, err := a.FetchPosting(context.Background(), d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}, ref)
				if err == nil || res.Text != "" {
					t.Fatalf("unchecked original %+v %v", res, err)
				}
			})
		}
	}
}
func nextMobileDetailFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	// Reuse the public query fixture with a response interceptor that mutates the
	// requested first row, including the one-column cloud original.
	backend := nextFixture(t, adapter, "")
	t.Cleanup(backend.Close)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RequestURI = ""
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(backend.URL, "http://")
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer res.Body.Close()
		var v map[string]any
		if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
			t.Error(err)
			return
		}
		data := v["data"].(map[string]any)
		if rows, ok := data["jobList"].([]any); ok {
			for _, item := range rows {
				row := item.(map[string]any)
				if row["id"] == nextID(adapter, 1) {
					mutateNext(row, adapter, scenario)
				}
			}
		}
		w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
		_ = json.NewEncoder(w).Encode(v)
	}))
}
func TestNextCampusStrictScopeAndEmpty(t *testing.T) {
	for _, adapter := range nextAdapters {
		t.Run(adapter, func(t *testing.T) {
			site := expandedSite(adapter)
			if err := RecognizeCampusURL(site.URL); err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(site.URL)
			q := u.Query()
			q.Add("type", "intern")
			u.RawQuery = q.Encode()
			if RecognizeCampusURL(u.String()) == nil {
				t.Fatal("expanded unverified scope accepted")
			}
			calls := 0
			a := PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected network") })}}
			if _, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: "intern"}, d.WatchTarget{}); err == nil || calls != 0 {
				t.Fatal("unsupported scope reached network")
			}
			if _, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{WatchInput: d.WatchInput{Direction: "backend"}}); err == nil || calls != 0 {
				t.Fatal("unsupported remote direction filter accepted")
			}
			srv := nextFixture(t, adapter, "empty")
			defer srv.Close()
			a.Client = nextClient(t, srv, adapter)
			refs, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
			if err != nil || len(refs) != 0 {
				t.Fatalf("verified empty scope %v %v", refs, err)
			}
		})
	}
}
func TestMobilePublicNonceAndNarrowMedia(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 10; i++ {
		h, err := newMobileHeader()
		if err != nil || seen[h.Digest] || seen[h.Conversation] {
			t.Fatal("public queries reused nonce", err)
		}
		seen[h.Digest] = true
		seen[h.Conversation] = true
	}
	for _, v := range []struct {
		adapter, method, path, media string
		want                         bool
	}{
		{"cmcloud", "POST", "/job-app/job/searchJobs.do", "text/plain", true},
		{"cmiot", "POST", "/job-app/company/getCompanyList.do", "text/plain", true},
		{"cmhome", "GET", "/job-app/job/searchJobs.do", "text/plain", false},
		{"pingan_tech", "POST", "/job-app/job/searchJobs.do", "text/plain", false},
		{"cmhome", "POST", "/job-app/login/getUserInfo.do", "text/plain", false},
		{"cmhome", "POST", "/job-app/job/searchJobs.do", "text/html", false},
	} {
		if mobilePlainJSON(v.adapter, v.method, v.path, v.media) != v.want {
			t.Fatalf("unexpected MIME exception %+v", v)
		}
	}
}

func TestMobilePublicWrappedJSON(t *testing.T) {
	for _, adapter := range []string{"cmcloud", "cmiot", "cmhome"} {
		t.Run(adapter, func(t *testing.T) {
			srv := nextFixture(t, adapter, "wrapped")
			defer srv.Close()
			a := PublicPlatform{Client: nextClient(t, srv, adapter)}
			refs, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
			if err != nil || len(refs) != 21 {
				t.Fatalf("wrapped public response %d %v", len(refs), err)
			}
		})
	}
	for _, raw := range []string{`null`, `[]`, `"not JSON"`, `"\"{}\""`} {
		var v mobileEnvelope[map[string]any]
		if json.Unmarshal([]byte(raw), &v) == nil {
			t.Fatalf("unbounded/non-object wrapper accepted %s", raw)
		}
	}
}
func TestMihoyoOptionalGraduateWindow(t *testing.T) {
	for _, missing := range []bool{false, true} {
		row := nextRow("mihoyo", 1)
		if missing {
			delete(row, "objectId")
			row["objectName"] = ""
		}
		b, _ := json.Marshal(row)
		var job mihoyoJob
		_ = json.Unmarshal(b, &job)
		if _, err := mihoyoRef(job); err != nil {
			t.Fatal("valid graduate project requires optional target", err)
		}
	}
	for _, target := range []struct{ id, name string }{{"", mihoyoTarget}, {"19", ""}, {"19", "2026届"}, {"20", mihoyoTarget}} {
		row := nextRow("mihoyo", 1)
		row["objectId"], row["objectName"] = target.id, target.name
		b, _ := json.Marshal(row)
		var job mihoyoJob
		_ = json.Unmarshal(b, &job)
		if _, err := mihoyoRef(job); err == nil {
			t.Fatalf("conflicting target accepted %+v", target)
		}
	}
}
