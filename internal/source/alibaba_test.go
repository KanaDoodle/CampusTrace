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

const fixtureCSRF = "12345678-1234-1234-1234-123456789012"

func alibabaFixture(t *testing.T, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/campus/position" {
			if r.Header.Get("Cookie") != "" {
				t.Error("bootstrap received a reused session")
			}
			w.Header().Set("Content-Type", "text/html")
			if scenario == "redirect" {
				w.Header().Set("Location", "https://example.org/?_csrf=secret")
				w.WriteHeader(302)
				return
			}
			if scenario == "blocked" {
				w.WriteHeader(403)
				return
			}
			if scenario == "large_bootstrap" {
				fmt.Fprint(w, strings.Repeat("a", (1<<20)+1))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: fixtureCSRF, Path: "/"})
			if scenario != "no_token" {
				fmt.Fprintf(w, `<script>window.__sysconfig={__token__: "%s"}</script>`, fixtureCSRF)
			}
			return
		}
		cookie, err := r.Cookie("SESSION")
		if r.Method != "POST" || r.URL.Query().Get("_csrf") != fixtureCSRF || err != nil || cookie.Value != fixtureCSRF {
			t.Error("missing public anonymous session or issued CSRF token")
		}
		w.Header().Set("Content-Type", "application/json")
		if scenario == "relogin" {
			json.NewEncoder(w).Encode(map[string]any{"success": false, "relogin": true})
			return
		}
		if r.URL.Path == "/searchCondition/listBatch" {
			name := alibabaBatchName
			if scenario == "scope_changed" {
				name = "阿里巴巴实习招聘"
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "content": map[string]any{"graduate": []any{map[string]any{"id": alibabaBatch, "name": name, "type": "graduate"}}}})
			return
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["channel"] != "new_campus_group_official_site" || input["language"] != "zh" {
			t.Error("wrong public channel")
		}
		row := func(n int) map[string]any {
			return map[string]any{"id": n, "batchId": alibabaBatch, "batchName": alibabaBatchName, "name": "Go 后端开发 " + strconv.Itoa(n), "status": "recruit", "categoryType": "freshman", "workLocations": []string{"杭州"}, "circleNames": []string{"阿里云", "淘天集团"}, "description": "开发服务", "requirement": "熟悉 Go", "graduationTime": map[string]int64{"from": 1793491200000, "to": 1824940800000}}
		}
		if r.URL.Path == "/position/detail" {
			id, _ := strconv.Atoi(input["id"].(string))
			v := row(id)
			if scenario == "wrong_id" {
				v["id"] = id + 1
			}
			if scenario == "wrong_detail_scope" {
				v["batchId"] = 100000560002
			}
			if scenario == "empty_text" {
				v["requirement"] = ""
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "content": v})
			return
		}
		if r.URL.Path != "/position/search" {
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
		if input["batchId"] != float64(alibabaBatch) || input["pageSize"] != float64(alibabaPageSize) {
			t.Error("wrong public batch/page size")
		}
		page := int(input["pageIndex"].(float64))
		total := 51
		rows := []any{}
		for n := (page-1)*alibabaPageSize + 1; n <= min(page*alibabaPageSize, total); n++ {
			rows = append(rows, row(n))
		}
		switch scenario {
		case "partial":
			rows = rows[:len(rows)-1]
		case "drift":
			if page == 2 {
				total = 52
			}
		case "duplicate":
			if page == 2 {
				rows[0] = row(1)
			}
		case "intern":
			rows[0].(map[string]any)["categoryType"] = "internship"
		case "capacity":
			total = 501
		}
		content := map[string]any{"datas": rows, "totalCount": total, "pageSize": alibabaPageSize, "currentPage": page}
		if scenario == "missing_total" {
			delete(content, "totalCount")
		}
		if scenario == "wrong_page" {
			content["currentPage"] = page + 1
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "content": content})
	}))
}
func TestAlibabaCompleteScopeOriginalTextAndFiltering(t *testing.T) {
	srv := alibabaFixture(t, "")
	defer srv.Close()
	keys := []string{}
	cacheCalls := 0
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}, Allow: func(_ context.Context, key string, limit int) (bool, error) {
		keys = append(keys, key)
		if limit != 30 {
			t.Errorf("limit %d", limit)
		}
		return true, nil
	}, CacheWrite: func(context.Context, string, string, HTTPEntry) error { cacheCalls++; return nil }}
	ctx := context.Background()
	v, err := a.PreviewCampus(ctx, AlibabaCampusURL)
	if err != nil || v.Total != 51 || v.Adapter != "alibaba" || v.MinimumInterval != 1800 || v.SupportsDirection {
		t.Fatalf("preview %+v %v", v, err)
	}
	s := d.Source{ID: "fixture", Adapter: "alibaba", Tenant: "100000760001"}
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	if err != nil || len(refs) != 51 {
		t.Fatalf("complete %d %v", len(refs), err)
	}
	rows, err := a.Discover(ctx, s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "开发 51"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("filtered %d %v", len(rows), err)
	}
	text, err := a.FetchPosting(ctx, s, refs[50])
	if err != nil || !strings.Contains(text.Text, "熟悉 Go") || !strings.Contains(text.Text, "阿里云、淘天集团") || !strings.Contains(text.Text, "2026年11月01日") {
		t.Fatalf("text %+v %v", text, err)
	}
	if a.Client.Jar != nil || cacheCalls != 0 {
		t.Fatal("anonymous session/token escaped operation")
	}
	for _, key := range keys {
		if key != "alibaba:public-site" {
			t.Errorf("non-shared pace %s", key)
		}
	}
	if _, err = a.Discover(ctx, s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil {
		t.Fatal("unsupported direction")
	}
}
func TestAlibabaRejectPartialScopeAndIdentityErrors(t *testing.T) {
	for _, scenario := range []string{"partial", "drift", "duplicate", "intern", "capacity", "missing_total", "wrong_page", "scope_changed", "no_token", "large_bootstrap", "blocked", "relogin", "redirect", "wrong_id", "wrong_detail_scope", "empty_text"} {
		t.Run(scenario, func(t *testing.T) {
			srv := alibabaFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{ID: "fixture", Adapter: "alibaba", Tenant: "100000760001"}
			var err error
			if strings.HasPrefix(scenario, "wrong_detail") || scenario == "wrong_id" || scenario == "empty_text" {
				_, err = a.FetchPosting(context.Background(), s, PostingRef{ExternalID: "51", URL: alibabaOrigin + "/campus/position/51"})
			} else {
				_, err = a.Discover(context.Background(), s, d.WatchTarget{})
			}
			if err == nil {
				t.Fatal("unsafe response accepted")
			}
		})
	}
}
func TestAlibabaStrictURLAndForgedRefBeforeRequest(t *testing.T) {
	if RecognizeCampusURL(AlibabaCampusURL) != nil {
		t.Fatal("canonical URL rejected")
	}
	for _, raw := range []string{alibabaOrigin + "/campus/index", AlibabaCampusURL + "&shareCode=abc", AlibabaCampusURL + "&batchId=100000560002", alibabaOrigin + "/campus/position?batchId=100000560002", "https://campus-talent.alibaba.com.evil.test/campus/position?batchId=100000760001"} {
		if RecognizeCampusURL(raw) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	calls := 0
	a := PublicPlatform{Client: &http.Client{Transport: queryTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, fmt.Errorf("unexpected request") })}}
	for _, s := range []d.Source{{Adapter: "alibaba", Tenant: "intern"}, {Adapter: "alibaba", Tenant: "100000760001"}} {
		_, err := a.FetchPosting(context.Background(), s, PostingRef{ExternalID: "51", URL: alibabaOrigin + "/campus/position/52"})
		if err == nil {
			t.Fatal("forged ref accepted")
		}
	}
	if calls != 0 {
		t.Fatal("forged reference reached network")
	}
}
func TestAlibabaLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("opt-in public network verification")
	}
	ctx := context.Background()
	a := PublicPlatform{}
	v, err := a.PreviewCampus(ctx, AlibabaCampusURL)
	if err != nil {
		t.Fatal(err)
	}
	s := d.Source{ID: "live-alibaba", Adapter: "alibaba", Tenant: v.ProjectCode}
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	if err != nil || len(refs) == 0 || len(refs) != v.Total {
		t.Fatalf("complete %d of %d: %v", len(refs), v.Total, err)
	}
	for _, r := range refs {
		if err := (p.Ingest{SourceID: s.ID, ExternalID: r.ExternalID, URL: r.URL, Title: r.Title, Company: r.Company, JobType: r.JobType, Locations: r.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []PostingRef{refs[0], refs[len(refs)-1]} {
		v, err := a.FetchPosting(ctx, s, r)
		if err != nil || !strings.Contains(v.Text, "任职资格") {
			t.Fatalf("detail %s: %v", r.ExternalID, err)
		}
	}
	t.Logf("company=阿里巴巴 complete_count=%d first=%s last=%s", len(refs), refs[0].ExternalID, refs[len(refs)-1].ExternalID)
}
