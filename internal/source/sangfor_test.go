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
)

func sangforFixture(t *testing.T, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Referer") != SangforCampusURL {
			t.Error("candidate session leaked or official referer missing")
		}
		bootstrap := r.URL.Path == "/api/api/connect/token"
		wantAuth := "Bearer synthetic-public-token"
		if bootstrap {
			wantAuth = ""
		}
		if r.Header.Get("Authorization") != wantAuth {
			t.Error("non-anonymous token used")
		}
		if scenario == "redirect" {
			http.Redirect(w, r, "https://example.invalid/login", 302)
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
		if bootstrap {
			v := map[string]any{"access_token": "synthetic-public-token", "token_type": "Bearer"}
			if scenario == "empty_token" {
				v["access_token"] = ""
			}
			if scenario == "bad_token_type" {
				v["token_type"] = "other"
			}
			if scenario == "token_newline" {
				v["access_token"] = "a\nb"
			}
			if scenario == "long_token" {
				v["access_token"] = strings.Repeat("x", 8193)
			}
			write(v)
			return
		}
		code := 0
		envelope := func(data any) {
			v := map[string]any{"code": code, "data": data}
			if scenario == "missing_code" {
				delete(v, "code")
			}
			write(v)
		}
		if r.URL.Path == "/api/api/Jobs/channel/100" {
			channel := map[string]any{"channelId": 101, "channelName": "27届校园招聘", "parentId": 100}
			if scenario == "changed_cohort" {
				channel["channelName"] = "28届校园招聘"
			}
			if scenario == "wrong_parent" {
				channel["parentId"] = 200
			}
			rows := []any{channel}
			if scenario == "duplicate_channel" {
				rows = append(rows, channel)
			}
			envelope(rows)
			return
		}
		row := func(n int, detail bool) map[string]any {
			v := map[string]any{"positionId": n, "channelIds": []int{100, 101}, "title": fmt.Sprintf("后端工程师 %d", n), "positionState": "open", "description": "<p>负责 Go 后台研发。</p><p>要求：Go、Linux，2027届。</p>", "commitment": "全职", "education": "本科及以上", "workPlaceText": "深圳市,北京市"}
			if scenario == "mixed_scope" || detail && scenario == "detail_scope" {
				v["channelIds"] = []int{100, 102}
			}
			if scenario == "xstar" {
				v["channelIds"] = []int{100, 101, 121}
			}
			if scenario == "inactive" {
				v["positionState"] = "closed"
			}
			if scenario == "internship" {
				v["commitment"] = "实习"
			}
			if detail {
				if scenario == "detail_id" {
					v["positionId"] = 999
				}
				if scenario == "detail_title" {
					v["title"] = "其他岗位"
				}
				if scenario == "empty_text" {
					v["description"] = "<p> </p>"
				}
			}
			return v
		}
		if r.URL.Path == "/api/api/Jobs" {
			var input map[string]any
			if json.NewDecoder(r.Body).Decode(&input) != nil || input["channelId"] != float64(101) || input["pageSize"] != float64(10) || input["kw"] != "" {
				t.Error("campus scope or full scan lost")
			}
			page := int(input["page"].(float64))
			total := 11
			if scenario == "empty" {
				total = 0
			}
			if scenario == "capacity" {
				total = MaxPostings + 1
			}
			if scenario == "drift" && page == 2 {
				total++
			}
			rows := []any{}
			for n := (page-1)*10 + 1; n <= min(page*10, total); n++ {
				id := n
				if scenario == "duplicate" && page == 2 {
					id = 1
				}
				rows = append(rows, row(id, false))
			}
			if scenario == "truncated" && len(rows) > 0 {
				rows = rows[:len(rows)-1]
			}
			data := map[string]any{"count": total, "listData": rows}
			if scenario == "missing_total" {
				delete(data, "count")
			}
			envelope(data)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/api/Jobs/") {
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/api/Jobs/"))
			envelope(map[string]any{"listData": row(n, true)})
			return
		}
		t.Error("unexpected public request path")
		w.WriteHeader(404)
	}))
}

func TestSangforCompleteScopeSessionAndOriginal(t *testing.T) {
	srv := sangforFixture(t, "")
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(SangforCampusURL)
	jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
	client := &http.Client{Transport: rewriteTransport{srv.URL}, Jar: jar}
	a := PublicPlatform{Client: client, sangforToken: "old-token", Allow: func(_ context.Context, key string, limit int) (bool, error) {
		if key != "sangfor:public-site" || limit != 30 {
			t.Error("source pacing lost")
		}
		return true, nil
	}, CacheRead: func(_ context.Context, _ string, key string) (HTTPEntry, error) {
		if key == d.Hash((PublicPlatform{}).Version()+":"+sangforAPI+"/connect/token") {
			t.Error("bootstrap cached")
		}
		return HTTPEntry{}, nil
	}, CacheWrite: func(_ context.Context, _ string, key string, _ HTTPEntry) error {
		if key == d.Hash((PublicPlatform{}).Version()+":"+sangforAPI+"/connect/token") {
			t.Error("bootstrap persisted")
		}
		return nil
	}}
	s := d.Source{ID: d.ID(), Adapter: "sangfor", Tenant: sangforScope}
	ctx := context.Background()
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	if err != nil || len(refs) != 11 {
		t.Fatalf("complete scan %d %v", len(refs), err)
	}
	selected, err := a.Discover(ctx, s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "后端工程师 11"}})
	if err != nil || len(selected) != 1 || selected[0].ExternalID != "11" {
		t.Fatal("last page lost before filtering", err)
	}
	preview, err := a.PreviewCampus(ctx, SangforCampusURL)
	if err != nil || preview.Total != 11 || preview.ProjectCode != sangforScope {
		t.Fatal("invalid preview", err)
	}
	for _, ref := range []PostingRef{refs[0], refs[10]} {
		res, err := a.FetchPosting(ctx, s, ref)
		if err != nil || res.Status != "SUCCESS" || !strings.Contains(res.Text, "Go 后台研发") || !strings.Contains(res.Text, "Go、Linux") || strings.Contains(res.Text, "<p>") || len(ref.Locations) != 2 {
			t.Fatal("original or cities lost", res, err)
		}
	}
	if client.Jar != jar || a.sangforToken != "old-token" {
		t.Fatal("caller session mutated")
	}
}

func TestSangforRejectsUnverifiableScopesAndDetails(t *testing.T) {
	for _, scenario := range []string{"empty_token", "bad_token_type", "token_newline", "long_token", "changed_cohort", "wrong_parent", "duplicate_channel", "missing_code", "mixed_scope", "xstar", "inactive", "internship", "duplicate", "truncated", "drift", "missing_total", "capacity", "redirect", "blocked"} {
		t.Run(scenario, func(t *testing.T) {
			srv := sangforFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			refs, err := a.Discover(context.Background(), d.Source{Adapter: "sangfor", Tenant: sangforScope}, d.WatchTarget{})
			if err == nil || refs != nil {
				t.Fatal("invalid or partial scope accepted")
			}
		})
	}
	for _, scenario := range []string{"detail_scope", "detail_id", "detail_title", "empty_text"} {
		t.Run(scenario, func(t *testing.T) {
			srv := sangforFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{Adapter: "sangfor", Tenant: sangforScope}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil {
				t.Fatal(err)
			}
			if res, err := a.FetchPosting(context.Background(), s, refs[0]); err == nil || res.Status == "SUCCESS" {
				t.Fatal("unbound original accepted")
			}
		})
	}
}

func TestSangforEmptyAndStrictEntry(t *testing.T) {
	srv := sangforFixture(t, "empty")
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	s := d.Source{Adapter: "sangfor", Tenant: sangforScope}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil || refs == nil || len(refs) != 0 {
		t.Fatal("empty scope rejected", err)
	}
	for _, raw := range []string{SangforCampusURL + "?invite=private", sangforOrigin + "/campucompon/schoolRecruitment/trainee", "https://hr.sangfor.com.evil.test/campucompon/schoolRecruitment"} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("unverified URL accepted")
		}
	}
	a.Client = &http.Client{Transport: noImportNetwork{t}}
	s.Tenant = "intern"
	if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil {
		t.Fatal("wrong tenant accepted")
	}
	s.Tenant = sangforScope
	if _, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "backend"}}); err == nil {
		t.Fatal("unknown direction accepted")
	}
	if _, err := a.FetchPosting(context.Background(), s, PostingRef{ExternalID: "1", URL: "https://other.invalid/detail"}); err == nil {
		t.Fatal("arbitrary detail reached network")
	}
}
