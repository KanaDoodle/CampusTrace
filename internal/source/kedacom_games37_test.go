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

func nextCampusFixture(t *testing.T, adapter, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" || req.Header.Get("Referer") != expandedURL(adapter) {
			t.Error("public read-only request boundary lost")
		}
		if scenario == "redirect" {
			http.Redirect(w, req, "https://example.invalid/login", 302)
			return
		}
		page, _ := strconv.Atoi(req.URL.Query().Get("PageIndex"))
		size, total := 15, 16
		if adapter == "games37" {
			page, _ = strconv.Atoi(req.URL.Query().Get("page"))
			size, total = 2, 3
		}
		if scenario == "empty" {
			total = 0
		}
		if scenario == "capacity" {
			total = MaxPostings + 1
		}
		if scenario == "drift" && page == 2 {
			total++
		}
		if adapter == "kedacom" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if strings.HasPrefix(req.URL.Path, "/zpdetail/") {
				id := strings.TrimPrefix(req.URL.Path, "/zpdetail/")
				name := "后端工程师 " + id
				category := "校园招聘"
				requirements := "Go、Linux；Redis 加分"
				if scenario == "detail_id" {
					id = "999"
				}
				if scenario == "detail_title" {
					name = "其他岗位"
				}
				if scenario == "detail_scope" {
					category = "实习生招聘"
				}
				if scenario == "detail_empty" {
					requirements = ""
				}
				fmt.Fprintf(w, `<div class="boxSupertitle"><span>%s<b class="applyStatus"></b></span></div><div class="xiangqingcontain"><ul><li class="ntitle">招聘类别：</li><li>%s</li><li class="ntitle">工作性质：</li><li>全职</li><li class="ntitle">截止时间：</li><li>2026-12-31</li></ul><div class="xiangqingtext"><p class="title">工作职责：</p><p>开发 Go 服务<br>处理并发请求</p><p class="title">任职资格：</p><p>%s</p></div><a id="apply" url="/Portal/Resume/ResumeItem?jid=%s">申请</a></div>`, name, category, requirements, id)
				return
			}
			if req.URL.Path != "/campus/" || req.URL.Query().Get("r") != "2" {
				t.Error("campus category filter lost")
			}
			fmt.Fprint(w, `<table class="jobsTable"><tr class="title"><td>职位名称</td><td>职位类型</td><td>工作地点</td><td>发布时间</td></tr>`)
			for n := (page-1)*size + 1; n <= min(page*size, total); n++ {
				if scenario == "truncated" && page == 2 {
					continue
				}
				id := n
				if scenario == "duplicate" && page == 2 {
					id = 1
				}
				href := fmt.Sprintf("/zpdetail/%d?r=2&amp;PageIndex=%d", id, page)
				if scenario == "scope" {
					href = fmt.Sprintf("/zpdetail/%d?r=3&amp;PageIndex=%d", id, page)
				}
				if scenario == "foreign" {
					href = "https://example.invalid/job/1"
				}
				fmt.Fprintf(w, `<tr><td><a title="后端工程师 %d" href="%s">后端工...</a></td><td></td><td title="上海市,江苏省-苏州市">上海市,...</td><td>2026-07-20</td></tr>`, id, href)
			}
			current := page
			if scenario == "page" {
				current++
			}
			fmt.Fprintf(w, `</table><div class="counts">共%d条记录</div><div class="tablefooter">当前第%d/%d页</div>`, total, current, max(1, (total+size-1)/size))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		q := req.URL.Query()
		if req.URL.Path != "/index.php" || q.Get("m") != "Home" || q.Get("c") != "campus" || q.Get("a") != "getIndexPage" || q.Get("post_type") != "" || q.Get("place_type") != "" {
			t.Error("official campus query lost")
		}
		if scenario == "search_empty" && q.Get("key") != "" {
			total = 0
		}
		rows := []any{}
		for n := (page-1)*size + 1; n <= min(page*size, total); n++ {
			if scenario == "truncated" && page == 2 {
				continue
			}
			id := n
			if scenario == "duplicate" && page == 2 {
				id = 1
			}
			category, jobURL, text := "校园招聘", games37JobPrefix+moreID("games37", id), "【岗位职责】开发 Go 服务\n【任职要求】Go、Linux；Redis 加分"
			if scenario == "scope" {
				category = "社会招聘"
			}
			if scenario == "foreign" {
				jobURL = "https://example.invalid/job/1"
			}
			if scenario == "missing_requirements" {
				text = "【岗位职责】开发 Go 服务"
			}
			rows = append(rows, map[string]any{"url": jobURL, "name": fmt.Sprintf("后端工程师 %d", id), "work_place": "广州,上海", "pname": "研发", "cate_name": category, "duty": text})
		}
		current := page
		if scenario == "page" {
			current++
		}
		v := map[string]any{"code": 0, "data": map[string]any{"list": rows, "count": total, "page": current, "pageSize": size, "pageCount": (total + size - 1) / size}}
		if scenario == "schema" {
			delete(v, "code")
		}
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Error(err)
		}
	}))
}

func TestNextCampusFullPaginationAndOriginals(t *testing.T) {
	for _, adapter := range []string{"kedacom", "games37"} {
		t.Run(adapter, func(t *testing.T) {
			srv := nextCampusFixture(t, adapter, "")
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			u, _ := url.Parse(expandedURL(adapter))
			jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
			client := &http.Client{Transport: rewriteTransport{srv.URL}, Jar: jar}
			a := PublicPlatform{Client: client}
			s := d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			want := 16
			if adapter == "games37" {
				want = 3
			}
			if err != nil || len(refs) != want {
				t.Fatalf("full scan %d %v", len(refs), err)
			}
			selected, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: fmt.Sprintf("后端工程师 %d", want)}})
			if err != nil || len(selected) != 1 {
				t.Fatal("last page lost before filtering", err)
			}
			preview, err := a.PreviewCampus(context.Background(), expandedURL(adapter))
			if err != nil || preview.Total != want || preview.ProjectCode != s.Tenant {
				t.Fatal("preview lost scope", err)
			}
			for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
				res, err := a.FetchPosting(context.Background(), s, ref)
				if err != nil || res.Status != "SUCCESS" || !strings.Contains(res.Text, "Go 服务") || !strings.Contains(res.Text, "Go、Linux") || strings.Contains(res.Text, "<p>") || ref.JobType != "UNKNOWN" || len(ref.Locations) != 2 {
					t.Fatal("original, city or unknown employment lost", res, err)
				}
				if adapter == "kedacom" && !strings.Contains(res.Text, "2026-12-31") {
					t.Fatal("published deadline lost")
				}
			}
			if client.Jar != jar {
				t.Fatal("caller session mutated")
			}
		})
	}
}

func TestNextCampusRejectsPartialAndForeignResults(t *testing.T) {
	for _, adapter := range []string{"kedacom", "games37"} {
		for _, scenario := range []string{"drift", "truncated", "duplicate", "scope", "foreign", "page", "capacity", "redirect"} {
			t.Run(adapter+"/"+scenario, func(t *testing.T) {
				srv := nextCampusFixture(t, adapter, scenario)
				defer srv.Close()
				a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
				refs, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
				if err == nil || refs != nil {
					t.Fatal("invalid or partial result accepted")
				}
			})
		}
	}
	for _, scenario := range []string{"detail_scope", "detail_id", "detail_title", "detail_empty"} {
		t.Run(scenario, func(t *testing.T) {
			srv := nextCampusFixture(t, "kedacom", scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{Adapter: "kedacom", Tenant: moreTenant("kedacom")}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil {
				t.Fatal(err)
			}
			res, err := a.FetchPosting(context.Background(), s, refs[0])
			if err == nil || res.Text != "" {
				t.Fatal("invalid detail accepted")
			}
		})
	}
	for _, scenario := range []string{"schema", "missing_requirements"} {
		t.Run(scenario, func(t *testing.T) {
			srv := nextCampusFixture(t, "games37", scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			refs, err := a.Discover(context.Background(), d.Source{Adapter: "games37", Tenant: "campus"}, d.WatchTarget{})
			if err == nil || refs != nil {
				t.Fatal("incomplete JD accepted")
			}
		})
	}
}

func TestNextCampusEmptyAndScopeValidation(t *testing.T) {
	for _, adapter := range []string{"kedacom", "games37"} {
		srv := nextCampusFixture(t, adapter, "empty")
		a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
		refs, err := a.Discover(context.Background(), d.Source{Adapter: adapter, Tenant: moreTenant(adapter)}, d.WatchTarget{})
		srv.Close()
		if err != nil || refs == nil || len(refs) != 0 {
			t.Fatal("valid empty result rejected", err)
		}
		if _, err := PlatformURL(d.Source{Adapter: adapter, Tenant: "wrong_scope"}); err == nil {
			t.Fatal("unverified scope accepted")
		}
	}
	for _, raw := range []string{kedacomOrigin + "/campus/?r=3", kedacomOrigin + "/campus/?r=2&r=3", kedacomOrigin + "/social", games37Origin + "/index.php?m=Home&c=society&a=getIndexPage", Games37CampusURL + "&candidate=private"} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("foreign recruiting scope accepted", raw)
		}
	}
}

func TestGames37SearchFallbackStillBindsOriginalIDAndTitle(t *testing.T) {
	srv := nextCampusFixture(t, "games37", "search_empty")
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	s := d.Source{Adapter: "games37", Tenant: "campus"}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil || len(refs) != 3 {
		t.Fatal("full list unavailable", err)
	}
	res, err := a.FetchPosting(context.Background(), s, refs[2])
	if err != nil || res.Status != "SUCCESS" || !strings.Contains(res.Text, refs[2].Title) {
		t.Fatal("empty keyword result lost existing job", res, err)
	}
	wrongTitle := refs[2]
	wrongTitle.Title = "其他岗位"
	if res, err := a.FetchPosting(context.Background(), s, wrongTitle); err == nil || res.Text != "" {
		t.Fatal("fallback expanded evidence to another title")
	}
	wrongID := refs[2]
	wrongID.ExternalID = moreID("games37", 99)
	wrongID.URL = games37JobPrefix + wrongID.ExternalID
	if res, err := a.FetchPosting(context.Background(), s, wrongID); err == nil || res.Text != "" {
		t.Fatal("fallback expanded evidence to another UUID")
	}
}
