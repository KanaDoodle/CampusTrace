package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func portalFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "getProjectList") {
			graduate, intern := map[string]any{"code": "present", "release": true, "groupList": []any{map[string]any{"planMapList": []any{map[string]int{"id": 56}}}}}, map[string]any{"code": "internship", "release": true, "groupList": []any{map[string]any{"planMapList": []any{map[string]int{"id": 45}}}}}
			if scenario == "closed_project" {
				graduate["release"] = false
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "body": map[string]any{"projectList": []any{graduate, intern}}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "navigation/list") {
			title := "网易互联网2027届校园招聘"
			if scenario == "closed_project" {
				title = "网易互联网实习招聘"
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": []any{map[string]any{"title": "应届生", "children": []any{map[string]string{"title": title, "link": NeteaseCampusURL}}}}})
			return
		}
		page, id := 1, ""
		if adapter == "jd" {
			if r.Method != "POST" {
				t.Error("JD requires published POST query")
			}
			if strings.Contains(r.URL.Path, "/detail/") {
				id = "11"
			} else {
				var input struct {
					PageIndex int `json:"pageIndex"`
					PageSize  int `json:"pageSize"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
				}
				if input.PageSize != 10 || r.URL.Query().Get("type") != "present" {
					t.Error("wrong JD scope")
				}
				page = input.PageIndex + 1
			}
		} else {
			if r.URL.Path != "/api/campuspc/position/getJobList" || r.Method != "GET" {
				t.Error("must only read Netease public list, never login-only details")
			}
			if r.URL.Query().Get("projectId") != "103" {
				t.Error("wrong Netease project")
			}
			page, _ = strconv.Atoi(r.URL.Query().Get("currentPage"))
			id = r.URL.Query().Get("positionIdList")
		}
		row := func(n int) map[string]any {
			if adapter == "jd" {
				return map[string]any{"publishId": n, "planId": 56, "positionName": "Go 后端开发 " + strconv.Itoa(n), "recruitType": "应届生", "workContent": `开发服务\n维护 API`, "qualification": "熟悉 Go", "requirementVoList": []any{map[string]string{"workCity": "北京"}, map[string]string{"workCity": "北京"}}}
			}
			return map[string]any{"id": n, "projectId": 103, "positionName": "Go 后端开发 " + strconv.Itoa(n), "workPlaceName": "杭州", "positionDescription": "开发服务", "positionRequirement": "熟悉 Go"}
		}
		rows := []any{}
		total := 11
		if id != "" {
			rows = append(rows, row(11))
			total = 1
		} else {
			for n := (page-1)*10 + 1; n <= min(page*10, 11); n++ {
				rows = append(rows, row(n))
			}
		}
		switch scenario {
		case "partial":
			rows = rows[:len(rows)-1]
		case "drift":
			if page == 2 {
				total = 12
			}
		case "capacity":
			total = 501
		case "duplicate":
			if page == 2 {
				if adapter == "jd" {
					rows[0].(map[string]any)["publishId"] = 1
				} else {
					rows[0].(map[string]any)["id"] = 1
				}
			}
		case "intern":
			if adapter == "jd" {
				rows[0].(map[string]any)["planId"] = 45
				rows[0].(map[string]any)["recruitType"] = "实习生"
			} else {
				rows[0].(map[string]any)["projectId"] = 75
			}
		case "missing_total":
			total = -1
		}
		if adapter == "jd" {
			body := any(map[string]any{"totalNumber": total, "items": rows, "pageCount": 0})
			if total == -1 {
				body = map[string]any{"items": rows}
			}
			if id != "" {
				body = row(11)
				if scenario == "intern" {
					body.(map[string]any)["recruitType"] = "实习生"
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "body": body})
		} else {
			data := map[string]any{"total": total, "pages": (total + 9) / 10, "list": rows}
			if total == -1 {
				delete(data, "total")
			}
			if scenario == "blocked" {
				json.NewEncoder(w).Encode(map[string]any{"code": 401, "data": nil})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": data})
		}
	}))
}

func TestPortalCampusSourcesPaginationOriginalTextAndScope(t *testing.T) {
	for _, adapter := range []string{"jd", "netease"} {
		t.Run(adapter, func(t *testing.T) {
			srv := portalFixture(t, adapter, "")
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			raw := JDCampusURL
			tenant := "present"
			if adapter == "netease" {
				raw = NeteaseCampusURL
				tenant = "103"
			}
			v, err := a.PreviewCampus(context.Background(), raw)
			if err != nil || v.Total != 11 || len(v.Samples) != 5 || v.Adapter != adapter || v.SupportsDirection {
				t.Fatalf("preview %+v %v", v, err)
			}
			s := d.Source{ID: "fixture", Adapter: adapter, Tenant: tenant}
			rows, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(rows) != 11 {
				t.Fatalf("rows %d %v", len(rows), err)
			}
			filtered, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "开发 11"}})
			if err != nil || len(filtered) != 1 {
				t.Fatalf("filtered %+v %v", filtered, err)
			}
			result, err := a.FetchPosting(context.Background(), s, rows[10])
			if err != nil || !strings.Contains(result.Text, "熟悉 Go") {
				t.Fatalf("original text %+v %v", result, err)
			}
			if adapter == "jd" && (!strings.Contains(result.Text, "开发服务\n维护 API") || len(rows[10].Locations) != 1) {
				t.Fatal("JD escaped linebreak or duplicate city was not normalized")
			}
			if _, err = a.FetchPosting(context.Background(), s, rows[0]); err == nil {
				t.Fatal("accepted mismatched posting identity")
			}
			forged := rows[10]
			forged.URL = "https://evil.example/"
			if _, err = a.FetchPosting(context.Background(), s, forged); err == nil {
				t.Fatal("accepted forged posting URL")
			}
			if _, err = a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil {
				t.Fatal("ignored unsupported direction")
			}
		})
	}
}

func TestJDExplicitProvinceCityLabelsMatchSavedCityPreferences(t *testing.T) {
	var row jdPost
	err := json.Unmarshal([]byte(`{"publishId":1,"planId":56,"positionName":"Go 开发","requirementVoList":[{"workCity":"北京市-北京市"},{"workCity":"浙江省-杭州市"},{"workCity":"海外-未知地点"}]}`), &row)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := jdRef(row)
	if err != nil || len(ref.Locations) != 3 || !d.CityAlternatives("北京|杭州", ref.Locations) || ref.Locations[2] != "海外-未知地点" {
		t.Fatalf("city preference %+v %v", ref, err)
	}
}
func TestPortalCampusSourcesRejectIncompleteOrWrongScope(t *testing.T) {
	for _, adapter := range []string{"jd", "netease"} {
		for _, scenario := range []string{"partial", "drift", "capacity", "duplicate", "intern", "closed_project", "missing_total"} {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := portalFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				tenant := "present"
				if adapter == "netease" {
					tenant = "103"
				}
				if _, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: tenant}, d.WatchTarget{}); err == nil {
					t.Fatal("accepted incomplete or wrong scope")
				}
			})
		}
	}
}
func TestPortalURLsRejectOtherProjectsCredentialsAndFilters(t *testing.T) {
	for _, raw := range []string{JDCampusURL, "https://campus.jd.com/", NeteaseCampusURL} {
		if RecognizeCampusURL(raw) != nil {
			t.Fatal("rejected supported URL", raw)
		}
	}
	for _, raw := range []string{"https://campus.jd.com/#/jobs?type=internship", "https://campus.jd.com/#/jobs?type=present&city=1", "https://campus.jd.com/?token=secret", "https://user:secret@campus.jd.com/", "https://campus.jd.com.evil.example/", "https://campus.163.com/app/job/position?id=75", "https://campus.163.com/app/job/position?id=103&id=75", "https://campus.game.163.com/app/job/position?id=102", "https://campus.163.com/app/job/position?id=103&keyword=Go"} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("accepted other scope", raw)
		}
	}
}
func TestNeteaseRestrictedResponseNeverBecomesPublicSuccess(t *testing.T) {
	srv := portalFixture(t, "netease", "blocked")
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	_, err := a.PreviewCampus(context.Background(), NeteaseCampusURL)
	var fetch *FetchError
	if !AsFetchError(err, &fetch) || fetch.Category != "BLOCKED" {
		t.Fatalf("wrong restriction result %v", err)
	}
}
func TestPortalCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("explicit public website verification")
	}
	for _, adapter := range []string{"jd", "netease"} {
		t.Run(adapter, func(t *testing.T) {
			raw := JDCampusURL
			if adapter == "netease" {
				raw = NeteaseCampusURL
			}
			a := PublicPlatform{}
			ctx := context.Background()
			v, err := a.PreviewCampus(ctx, raw)
			if err != nil {
				t.Fatal(err)
			}
			s := d.Source{ID: "live-" + adapter, Adapter: adapter, Tenant: v.ProjectCode}
			rows, err := a.Discover(ctx, s, d.WatchTarget{})
			if err != nil || len(rows) != v.Total || len(rows) == 0 {
				t.Fatalf("complete count %d of %d: %v", len(rows), v.Total, err)
			}
			maxCities := 0
			for _, ref := range rows {
				maxCities = max(maxCities, len(ref.Locations))
				in := p.Ingest{Company: ref.Company, Title: ref.Title, SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, JobType: ref.JobType, Locations: ref.Locations, Text: "public original text", FetchStatus: "SUCCESS"}
				if err := in.Validate(); err != nil {
					t.Fatalf("posting %s cannot enter ingest: %d cities, %v", ref.ExternalID, len(ref.Locations), err)
				}
			}
			for _, ref := range []PostingRef{rows[0], rows[len(rows)-1]} {
				result, err := a.FetchPosting(ctx, s, ref)
				if err != nil || !strings.Contains(result.Text, "任职资格") {
					t.Fatalf("text %s %v", ref.ExternalID, err)
				}
			}
			t.Log(fmt.Sprintf("company=%s complete_count=%d max_cities=%d first=%s last=%s", v.Company, len(rows), maxCities, rows[0].ExternalID, rows[len(rows)-1].ExternalID))
		})
	}
}
