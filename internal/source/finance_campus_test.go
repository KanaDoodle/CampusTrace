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

var financeCustomAdapters = []string{"bankcomm_tech", "cms_securities", "htsc_securities"}

func financeRow(adapter string, n int) map[string]any {
	if adapter == "bankcomm_tech" {
		project := bankcommITProject
		if n%2 == 0 {
			project = "2026年秋季校招（业务类）"
		}
		return map[string]any{"positionId": n, "pubName": fmt.Sprintf("软件开发工程师 %d", n), "engageType": "1", "bankNumber": bankcommHead, "bankName": "总行", "deptName": "软件开发中心", "deptNumber": 123, "projectName": project, "workPlace": "上海", "endDate": "2026-10-18", "responsibility": "<p>开发 Go 服务</p>", "require": "应届生；Go、Linux；英语六级；实习考察"}
	}
	project := securitiesProjects[adapter]
	id, _ := strconv.Atoi(project.Project)
	return map[string]any{"postId": fmt.Sprintf("%024x", n), "postName": fmt.Sprintf("金融科技工程师 %d", n), "recruitType": 1, "projectId": id, "projectName": project.Name, "workTypeStr": "全职", "workPlaceStr": "北京", "workContent": "<p>开发 Go 服务</p>", "serviceCondition": "2027届；Go、Linux；英语六级；实习考察", "endDate": "2026-10-18", "canDelivery": false}
}

func financeFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != expandedSite(adapter).URL {
			t.Error("public scope or session isolation lost")
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
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		page, size, total := 1, 15, 16
		detail := strings.Contains(r.URL.Path, "listPositionDetail")
		id := r.Form.Get("postId")
		if adapter == "bankcomm_tech" {
			if !strings.HasPrefix(r.URL.Path, "/api/GTMS.GTMS-PORTAL.V-1.0/") {
				t.Error("old bank gateway used")
			}
			var input struct {
				Head map[string]any `json:"REQ_HEAD"`
				Body struct {
					Params json.RawMessage `json:"params"`
				} `json:"REQ_BODY"`
			}
			if json.Unmarshal([]byte(r.Form.Get("REQ_MESSAGE")), &input) != nil || input.Head["ACCESS_TOKEN"] != nil || input.Head["REFRESH_TOKEN"] != nil {
				t.Error("candidate token or malformed envelope")
			}
			success := "1"
			if scenario == "missing_code" {
				success = ""
			}
			head := map[string]any{"TRAN_SUCCESS": success}
			if strings.HasSuffix(r.URL.Path, "queryOrgNameList.do") {
				row := map[string]any{"code": strconv.FormatInt(bankcommHead, 10), "name": "总行"}
				rows := []any{row}
				if scenario == "wrong_catalog" {
					row["name"] = "其他单位"
				}
				if scenario == "duplicate_catalog" {
					rows = append(rows, row)
				}
				write(map[string]any{"RSP_HEAD": head, "RSP_BODY": map[string]any{"results": []any{map[string]any{"list": rows}}}})
				return
			}
			detail = strings.HasSuffix(r.URL.Path, "queryPositionDetail.do")
			var params map[string]any
			if json.Unmarshal(input.Body.Params, &params) != nil {
				t.Fatal("missing public params")
			}
			if detail {
				id, _ = params["positionId"].(string)
			} else {
				business := params["businessPara"].(map[string]any)
				paging := params["pagePara"].(map[string]any)
				if business["engageType"] != float64(1) || business["bankNumber"] != strconv.FormatInt(bankcommHead, 10) || business["pubName"] != "" || paging["pageSize"] != float64(50) {
					t.Error("bank unit/campus scope or complete scan lost")
				}
				page, size, total = int(paging["pageNum"].(float64)), 50, 51
			}
		} else {
			project := securitiesProjects[adapter]
			if !strings.Contains(r.URL.Path, "/"+project.Suite) || r.Form.Get("recruitType") != "1" {
				t.Error("wrong securities tenant or campus category")
			}
			if !detail {
				if r.Form.Get("projectCode") != project.Project || r.Form.Get("pageSize") != "15" || r.Form.Get("isFrompb") != "true" {
					t.Error("wrong fixed securities project or page size")
				}
				page, _ = strconv.Atoi(r.Form.Get("currentPage"))
			}
		}
		mutate := func(row map[string]any, detail bool) {
			kind, unit, title, duties, requirements := "recruitType", "projectId", "postName", "workContent", "serviceCondition"
			if adapter == "bankcomm_tech" {
				kind, unit, title, duties, requirements = "engageType", "bankNumber", "pubName", "responsibility", "require"
			}
			if scenario == "mixed_scope" || (detail && scenario == "detail_scope") {
				row[unit] = 0
			}
			if scenario == "inactive" {
				if adapter == "bankcomm_tech" {
					row[kind] = "2"
				} else {
					row[kind] = 3
				}
			}
			if adapter != "bankcomm_tech" {
				if scenario == "changed_project" {
					row["projectName"] = "其他年度招聘"
				}
				if scenario == "internship" {
					row["workTypeStr"] = "实习"
				}
			}
			if detail {
				switch scenario {
				case "detail_title":
					row[title] = "其他岗位"
				case "detail_id":
					row["postId"] = fmt.Sprintf("%024x", 999)
				case "empty_duties":
					row[duties] = ""
				case "empty_requirements":
					row[requirements] = ""
				}
			}
		}
		var data any
		if detail {
			base := 10
			if adapter != "bankcomm_tech" {
				base = 16
			}
			n, _ := strconv.ParseInt(id, base, 32)
			row := financeRow(adapter, int(n))
			mutate(row, true)
			if adapter == "bankcomm_tech" {
				delete(row, "positionId")
				delete(row, "engageType")
				delete(row, "projectName")
			}
			data = row
		} else {
			if scenario == "capacity" {
				total = MaxPostings + 1
			}
			if scenario == "empty" {
				total = 0
			}
			if scenario == "drift" && page == 2 {
				total++
			}
			rows := []any{}
			for n := (page-1)*size + 1; n <= min(page*size, total); n++ {
				actual := n
				if scenario == "duplicate" && page == 2 {
					actual = 1
				}
				row := financeRow(adapter, actual)
				mutate(row, false)
				rows = append(rows, row)
			}
			if scenario == "truncated" && len(rows) > 0 {
				rows = rows[:len(rows)-1]
			}
			if adapter == "bankcomm_tech" {
				data = map[string]any{"total": total, "policyList": rows}
				if scenario == "missing_total" {
					delete(data.(map[string]any), "total")
				}
			} else {
				form := map[string]any{"dataCount": total, "pageData": rows, "currentPage": page, "pageSize": size, "totalPage": (total + size - 1) / size}
				if scenario == "missing_total" {
					delete(form, "dataCount")
				}
				if scenario == "page_echo" {
					form["currentPage"] = 0
				}
				data = map[string]any{"pageForm": form}
			}
		}
		if adapter == "bankcomm_tech" {
			write(map[string]any{"RSP_HEAD": map[string]any{"TRAN_SUCCESS": "1"}, "RSP_BODY": map[string]any{"results": data}})
		} else {
			state := "200"
			if scenario == "missing_code" {
				state = ""
			}
			write(map[string]any{"state": state, "type": "success", "data": data})
		}
	}))
}

func TestFinanceCampusCompleteScopesAndOriginals(t *testing.T) {
	for _, adapter := range financeCustomAdapters {
		t.Run(adapter, func(t *testing.T) {
			srv := financeFixture(t, adapter, "")
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(expandedSite(adapter).URL)
			jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
			client := &http.Client{Transport: rewriteTransport{srv.URL}, Jar: jar}
			a := PublicPlatform{Client: client, Allow: func(_ context.Context, key string, limit int) (bool, error) {
				want := "wecruit:public-site"
				if adapter == "bankcomm_tech" {
					want = adapter + ":public-site"
				}
				if key != want || limit != 30 {
					t.Error("shared official host pacing lost")
				}
				return true, nil
			}}
			ctx := context.Background()
			s := d.Source{ID: d.ID(), Adapter: adapter, Tenant: moreTenant(adapter)}
			refs, err := a.Discover(ctx, s, d.WatchTarget{})
			want := 16
			if adapter == "bankcomm_tech" {
				want = 26 // business rows removed only after both complete pages
			}
			if err != nil || len(refs) != want {
				t.Fatalf("complete scope: %d %v", len(refs), err)
			}
			preview, err := a.PreviewCampus(ctx, expandedSite(adapter).URL)
			if err != nil || preview.Total != want || preview.Name != expandedSite(adapter).Company+" · 校招" || len(preview.Name) > 160 {
				t.Fatalf("saveable preview: %+v %v", preview, err)
			}
			for _, ref := range refs {
				if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
					t.Fatal(err)
				}
			}
			for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
				res, err := a.FetchPosting(ctx, s, ref)
				if err != nil || res.Status != "SUCCESS" || !strings.Contains(res.Text, "实习考察") || !strings.Contains(res.Text, "英语六级") || !strings.Contains(res.Text, "2026-10-18") || strings.Contains(res.Text, "<p>") {
					t.Fatalf("independent original: %+v %v", res, err)
				}
				if adapter == "bankcomm_tech" && ref.JobType != "UNKNOWN" {
					t.Fatal("employment inferred from recruitment category")
				}
			}
			selected, err := a.Discover(ctx, s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: refs[len(refs)-1].Title}})
			if err != nil || len(selected) != 1 || selected[0].ExternalID != refs[len(refs)-1].ExternalID {
				t.Fatal("keyword dropped last-page result")
			}
			bad := refs[0]
			bad.ExternalID = "999999"
			if adapter == "bankcomm_tech" {
				bad.URL = bankcommJobURL(bad.ExternalID)
			} else {
				bad.URL = "https://other.example.invalid/detail"
			}
			if res, err := a.FetchPosting(ctx, s, bad); err == nil || res.Status == "SUCCESS" {
				t.Fatal("unbound detail accepted")
			}
			if client.Jar != jar {
				t.Fatal("caller session mutated")
			}
		})
	}
}

func TestFinanceCampusRejectsUnverifiableResponses(t *testing.T) {
	for _, adapter := range financeCustomAdapters {
		scenarios := []string{"mixed_scope", "inactive", "truncated", "duplicate", "capacity", "missing_code", "missing_total", "drift", "redirect", "http_blocked"}
		if adapter == "bankcomm_tech" {
			scenarios = append(scenarios, "wrong_catalog", "duplicate_catalog")
		} else {
			scenarios = append(scenarios, "page_echo", "changed_project", "internship")
		}
		for _, scenario := range scenarios {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := financeFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				refs, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
				if err == nil || refs != nil {
					t.Fatalf("invalid/partial scan accepted: %d %v", len(refs), err)
				}
			})
		}
		details := []string{"detail_scope", "detail_title", "empty_duties", "empty_requirements"}
		if adapter != "bankcomm_tech" {
			details = append(details, "detail_id")
		}
		for _, scenario := range details {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := financeFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				s := d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}
				refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
				if err != nil {
					t.Fatal(err)
				}
				if res, err := a.FetchPosting(context.Background(), s, refs[0]); err == nil || res.Status == "SUCCESS" {
					t.Fatal("invalid detail accepted")
				}
			})
		}
	}
}

func TestFinanceCampusEmptyAndStrictEntry(t *testing.T) {
	for _, adapter := range financeCustomAdapters {
		t.Run(adapter, func(t *testing.T) {
			srv := financeFixture(t, adapter, "empty")
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || refs == nil || len(refs) != 0 {
				t.Fatalf("valid empty scope rejected: %v", err)
			}
			u, _ := url.Parse(expandedSite(adapter).URL)
			q := u.Query()
			q.Set("inviteCode", "private")
			u.RawQuery = q.Encode()
			if RecognizeCampusURL(u.String()) == nil {
				t.Fatal("private URL accepted")
			}
			a.Client = &http.Client{Transport: noImportNetwork{t}}
			s.Tenant = "other_project"
			if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil {
				t.Fatal("wrong tenant accepted")
			}
			s.Tenant = moreTenant(adapter)
			if _, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil {
				t.Fatal("unsupported server direction accepted")
			}
		})
	}
}
