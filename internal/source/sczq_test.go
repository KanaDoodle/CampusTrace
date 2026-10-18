package source

import (
	"context"
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

func sczqFixture(t *testing.T, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.Header.Get("Cookie") != "" || req.Header.Get("Authorization") != "" || req.Header.Get("Referer") != SCZQCampusURL {
			t.Error("anonymous public boundary lost")
		}
		if scenario == "redirect" {
			http.Redirect(w, req, "https://example.invalid/login", 302)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if strings.HasPrefix(req.URL.Path, "/zpdetail/") {
			id := strings.TrimPrefix(req.URL.Path, "/zpdetail/")
			name, category, duties, requirements := "后端工程师 "+id, "校园招聘", "开发 Go 服务<br>处理并发请求", "Go、Linux；Redis 加分"
			if scenario == "detail_id" {
				id = "999"
			}
			if scenario == "detail_title" {
				name = "其他岗位"
			}
			if scenario == "detail_scope" {
				category = "社会招聘"
			}
			if scenario == "detail_empty" {
				requirements = ""
			}
			if scenario == "empty_duties" {
				duties = ""
			}
			if scenario == "detail_duplicate" {
				requirements += "</p><p>任职资格：</p><p>另一份要求"
			}
			apply := "/Portal/Resume/ResumeItem?jid=" + id + "&amp;r=%2Fzpdetail%2F" + id
			if scenario == "duplicate_jid" {
				apply += "&amp;jid=999"
			}
			if scenario == "foreign_apply" {
				apply = "https://example.invalid" + apply
			}
			fmt.Fprintf(w, `<div class="xiangqingtitle"><span>%s<b class="applyStatus"></b></span></div><div class="xiangqingcontain"><ul class="xiangqinglist"><li class="ntitle td-HasRecruitCategories">招聘类别：</li><li class="nvalue">%s</li><li class="ntitle">工作性质：</li><li class="nvalue">全职</li><li class="ntitle">发布时间：</li><li class="nvalue">2026-09-30</li><li class="ntitle">截止时间：</li><li class="nvalue">2026-12-31</li></ul><div class="xiangqingtext"><p>工作地点：</p><p>上海市,江苏省-苏州市</p><br><p>工作职责：</p><p>%s</p><br><p>任职资格：</p><p>%s</p></div><a id="apply" url="%s">现在申请</a></div>`, name, category, duties, requirements, apply)
			return
		}
		if req.URL.Path != "/campus/" || req.URL.Query().Get("r") != "2" || len(req.URL.Query()) != 2 {
			t.Error("full campus query changed or a city filter was added")
		}
		page, _ := strconv.Atoi(req.URL.Query().Get("PageIndex"))
		total := 16
		if scenario == "empty" {
			total = 0
		}
		if scenario == "capacity" {
			total = MaxPostings + 1
		}
		if scenario == "drift" && page > 1 {
			total++
		}
		fmt.Fprint(w, `<table class="listtable"><thead><tr class="tabletitle"><th>职位名称</th><th>职位类型</th><th>工作地点</th><th>发布时间</th></tr></thead>`)
		for n := (page-1)*15 + 1; n <= min(page*15, total); n++ {
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
			jobID := id
			if scenario == "list_id" {
				jobID = 999
			}
			fmt.Fprintf(w, `<tr><td><a title="后端工程师 %d" jobAdId="%d" href="%s">后端工...</a></td><td></td><td title="上海市,江苏省-苏州市">上海市,...</td><td>2026-09-30</td></tr>`, id, jobID, href)
		}
		current := page
		if scenario == "page" {
			current++
		}
		fmt.Fprint(w, `</table>`)
		if scenario != "missing_count" {
			fmt.Fprintf(w, `<span class="tablenote">共%d条记录</span>`, total)
		}
		fmt.Fprintf(w, `<div class="tablefooter"><span>当前第%d/%d页</span></div>`, current, max(1, (total+14)/15))
	}))
}

func TestSCZQFullPaginationAndIndependentDetails(t *testing.T) {
	srv := sczqFixture(t, "")
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(SCZQCampusURL)
	jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
	client := &http.Client{Transport: rewriteTransport{srv.URL}, Jar: jar}
	a := PublicPlatform{Client: client}
	s := d.Source{Adapter: "sczq_securities", Tenant: "campus_2"}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil || len(refs) != 16 {
		t.Fatal("incomplete campus list", len(refs), err)
	}
	filtered, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "后端工程师 16"}})
	if err != nil || len(filtered) != 1 || filtered[0].ExternalID != "16" {
		t.Fatal("last page lost before filtering", filtered, err)
	}
	preview, err := a.PreviewCampus(context.Background(), SCZQCampusURL)
	if err != nil || preview.Total != 16 || preview.ProjectCode != "campus_2" || preview.MinimumInterval != 1800 {
		t.Fatal("scope or interval lost", preview, err)
	}
	for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
		result, err := a.FetchPosting(context.Background(), s, ref)
		if err != nil || result.Status != "SUCCESS" || !strings.Contains(result.Text, "Go 服务") || !strings.Contains(result.Text, "Go、Linux") || !strings.Contains(result.Text, "2026-12-31") || !strings.Contains(result.Text, "官网工作性质：全职") || strings.Contains(result.Text, "<p>") || ref.JobType != "UNKNOWN" || len(ref.Locations) != 2 {
			t.Fatal("original text or employment boundary lost", result, ref, err)
		}
	}
	if client.Jar != jar {
		t.Fatal("caller session mutated")
	}
}

func TestSCZQRejectsIncompleteOrUnboundResults(t *testing.T) {
	for _, scenario := range []string{"drift", "truncated", "duplicate", "scope", "foreign", "page", "capacity", "redirect", "list_id", "missing_count"} {
		t.Run(scenario, func(t *testing.T) {
			srv := sczqFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			refs, err := a.Discover(context.Background(), d.Source{Adapter: "sczq_securities", Tenant: "campus_2"}, d.WatchTarget{})
			if err == nil || refs != nil {
				t.Fatal("partial or foreign list accepted", refs, err)
			}
		})
	}
	for _, scenario := range []string{"detail_scope", "detail_id", "detail_title", "detail_empty", "empty_duties", "detail_duplicate", "duplicate_jid", "foreign_apply"} {
		t.Run(scenario, func(t *testing.T) {
			srv := sczqFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{Adapter: "sczq_securities", Tenant: "campus_2"}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.FetchPosting(context.Background(), s, refs[0])
			if err == nil || result.Text != "" {
				t.Fatal("unbound detail accepted", result, err)
			}
		})
	}
}

func TestSCZQEmptyAndStrictEntry(t *testing.T) {
	srv := sczqFixture(t, "empty")
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	refs, err := a.Discover(context.Background(), d.Source{Adapter: "sczq_securities", Tenant: "campus_2"}, d.WatchTarget{})
	if err != nil || refs == nil || len(refs) != 0 {
		t.Fatal("empty campus list rejected", refs, err)
	}
	if _, err := PlatformURL(d.Source{Adapter: "sczq_securities", Tenant: "wrong_scope"}); err == nil {
		t.Fatal("wrong scope accepted")
	}
	for _, raw := range []string{SCZQCampusURL, sczqOrigin + "/campus/", sczqOrigin + "/Campus"} {
		site, err := campusSite(raw)
		if err != nil || site.Adapter != "sczq_securities" || site.URL != SCZQCampusURL {
			t.Fatal("legacy entry did not resolve", raw, site, err)
		}
	}
	for _, raw := range []string{SCZQCampusURL + "&c=1100", sczqOrigin + "/campus/?r=3", SCZQCampusURL + "&r=3", sczqOrigin + "/Social", "http://sczq.zhiye.com/campus/", "https://sczq.zhiye.com:443/campus/", SCZQCampusURL + "&candidate=private"} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("filtered or private entry accepted", raw)
		}
	}
}
