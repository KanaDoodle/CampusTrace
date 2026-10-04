package source

import (
	"context"
	"encoding/json"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func graduateFixture(t *testing.T, adapter string, change func(int, map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page, id := 1, ""
		if adapter == "baidu" {
			if r.Header.Get("Referer") != BaiduCampusURL {
				t.Error("missing public referer")
			}
			if r.Method == "POST" {
				if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Error("wrong form encoding")
				}
				r.ParseForm()
				page, _ = strconv.Atoi(r.Form.Get("curPage"))
				if r.Form.Get("recruitType") != "GRADUATE" || r.Form.Get("pageSize") != "10" {
					t.Error("wrong graduate scope")
				}
			} else {
				id = r.URL.Query().Get("postId")
				if r.URL.Query().Get("recruitType") != "GRADUATE" {
					t.Error("wrong detail scope")
				}
			}
		} else {
			if r.Header.Get("Referer") != MeituanCampusURL {
				t.Error("missing public referer")
			}
			var body struct {
				Page struct {
					Number int `json:"pageNo"`
					Size   int `json:"pageSize"`
				} `json:"page"`
				ID    string `json:"jobUnionId"`
				Types []struct {
					Code string `json:"code"`
				} `json:"jobType"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			id = body.ID
			page = body.Page.Number
			if id == "" && (body.Page.Size != 10 || len(body.Types) != 1 || body.Types[0].Code != "1") {
				t.Error("wrong graduate scope")
			}
		}
		row := func(n int) map[string]any {
			if adapter == "baidu" {
				return map[string]any{"postId": fmt.Sprintf("00000000-0000-0000-0000-%012d", n), "name": "Go 后端开发 " + strconv.Itoa(n), "workPlace": "北京、上海", "projectType": "校招", "projectTypeCode": "1", "workContent": "构建服务", "serviceCondition": "熟悉 Go"}
			}
			return map[string]any{"jobUnionId": strconv.Itoa(n), "name": "Go 后端开发 " + strconv.Itoa(n), "jobType": "1", "cityList": []any{map[string]string{"name": "北京"}}, "jobDuty": "构建服务", "jobRequirement": "熟悉 Go"}
		}
		var response map[string]any
		if id != "" {
			response = map[string]any{"status": "ok", "data": row(11)}
			if adapter == "meituan" {
				response["status"] = 1
			}
		} else {
			rows := []any{}
			for n := (page-1)*10 + 1; n <= min(page*10, 11); n++ {
				rows = append(rows, row(n))
			}
			if adapter == "baidu" {
				response = map[string]any{"status": "ok", "data": map[string]any{"total": "11", "pages": 2, "pageNum": page, "pageSize": 10, "list": rows}}
			} else {
				response = map[string]any{"status": 1, "data": map[string]any{"list": rows, "page": map[string]int{"totalCount": 11, "totalPage": 2, "pageNo": page, "pageSize": 10}}}
			}
		}
		if change != nil {
			change(page, response)
		}
		json.NewEncoder(w).Encode(response)
	}))
}

func TestGraduateSourcesScopePaginationDetailAndPreview(t *testing.T) {
	for _, site := range CampusSites()[1:] {
		t.Run(site.Adapter, func(t *testing.T) {
			srv := graduateFixture(t, site.Adapter, nil)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			preview, err := a.PreviewCampus(context.Background(), site.URL)
			if err != nil || preview.Total != 11 || len(preview.Samples) != 5 || preview.Adapter != site.Adapter || preview.SupportsDirection || preview.MinimumInterval != 1800 {
				t.Fatalf("preview %+v %v", preview, err)
			}
			s := d.Source{ID: "fixture", Adapter: site.Adapter, Tenant: preview.ProjectCode}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(refs) != 11 || refs[10].Company != site.Company || refs[10].JobType != "FULL_TIME" {
				t.Fatalf("discovery %+v %v", refs, err)
			}
			filtered, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "开发 11"}})
			if err != nil || len(filtered) != 1 || filtered[0].ExternalID != refs[10].ExternalID {
				t.Fatalf("keyword %+v %v", filtered, err)
			}
			result, err := a.FetchPosting(context.Background(), s, refs[10])
			if err != nil || !strings.Contains(result.Text, "熟悉 Go") || !strings.Contains(result.Text, "招聘项目") {
				t.Fatalf("detail %+v %v", result, err)
			}
			_, err = a.FetchPosting(context.Background(), s, refs[0])
			if err == nil {
				t.Fatal("accepted wrong detail identity")
			}
			_, err = a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}})
			if err == nil {
				t.Fatal("silently ignored unsupported direction")
			}
			forged := refs[10]
			forged.URL = "https://evil.example/"
			_, err = a.FetchPosting(context.Background(), s, forged)
			if err == nil {
				t.Fatal("accepted forged detail URL")
			}
		})
	}
}

func TestGraduateSourcesRejectPartialDriftingOverCapacityAndOtherScopes(t *testing.T) {
	for _, adapter := range []string{"baidu", "meituan"} {
		for _, scenario := range []string{"partial", "changed_total", "duplicate", "capacity", "intern"} {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := graduateFixture(t, adapter, func(page int, v map[string]any) {
					data := v["data"].(map[string]any)
					rows := data["list"].([]any)
					switch scenario {
					case "partial":
						data["list"] = rows[:len(rows)-1]
					case "changed_total":
						if page == 2 {
							if adapter == "baidu" {
								data["total"] = "12"
							} else {
								data["page"].(map[string]int)["totalCount"] = 12
							}
						}
					case "duplicate":
						if page == 2 {
							if adapter == "baidu" {
								rows[0].(map[string]any)["postId"] = "00000000-0000-0000-0000-000000000001"
							} else {
								rows[0].(map[string]any)["jobUnionId"] = "1"
							}
						}
					case "capacity":
						if adapter == "baidu" {
							data["total"] = "501"
						} else {
							data["page"].(map[string]int)["totalCount"] = 501
						}
					case "intern":
						if adapter == "baidu" {
							rows[0].(map[string]any)["projectTypeCode"] = "intern"
						} else {
							rows[0].(map[string]any)["jobType"] = "2"
						}
					}
				})
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				tenant := "GRADUATE"
				if adapter == "meituan" {
					tenant = "graduate"
				}
				_, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: tenant}, d.WatchTarget{})
				if err == nil {
					t.Fatal("accepted incomplete or incorrectly scoped scan")
				}
			})
		}
	}
}

func TestCampusURLScopesRejectCredentialsInternshipsAndUnknownFilters(t *testing.T) {
	for _, raw := range []string{BaiduCampusURL, MeituanCampusURL, XHSURL, "https://talent.baidu.com/jobs/campus/", "https://zhaopin.meituan.com/web/campus"} {
		if RecognizeCampusURL(raw) != nil {
			t.Fatal("rejected supported URL", raw)
		}
	}
	for _, raw := range []string{"https://talent.baidu.com/jobs/list?recruitType=INTERN", "https://talent.baidu.com/jobs/list?projectType=1", "https://talent.baidu.com/jobs/list?recruitType=GRADUATE&recruitType=INTERN", "https://zhaopin.meituan.com/web/campus?hiringType=2_1", "https://zhaopin.meituan.com:443/web/campus", "https://user:secret@talent.baidu.com/jobs/list", "https://talent.baidu.com/jobs/list#secret", "https://talent.baidu.com.evil.example/jobs/list"} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("accepted unsupported scope", raw)
		}
	}
}

func TestGraduateLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("explicit public website verification")
	}
	for _, site := range CampusSites()[1:] {
		t.Run(site.Adapter, func(t *testing.T) {
			a := PublicPlatform{}
			v, err := a.PreviewCampus(context.Background(), site.URL)
			if err != nil {
				t.Fatal(err)
			}
			s := d.Source{ID: "live-graduate-" + site.Adapter, Adapter: site.Adapter, Tenant: v.ProjectCode}
			rows, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(rows) != v.Total || len(rows) == 0 {
				t.Fatalf("count %d of %d: %v", len(rows), v.Total, err)
			}
			result, err := a.FetchPosting(context.Background(), s, rows[0])
			if err != nil || !strings.Contains(result.Text, "任职资格") {
				t.Fatalf("detail %+v %v", result, err)
			}
			t.Logf("company=%s complete_count=%d detail_id=%s", site.Company, len(rows), rows[0].ExternalID)
		})
	}
}
