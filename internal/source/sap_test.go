package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

func sapFixture(t *testing.T, scenario *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Referer") != SAPCampusURL {
			t.Error("candidate session inherited or public referer missing")
		}
		if *scenario == "blocked" {
			w.WriteHeader(403)
			return
		}
		if *scenario == "redirect" {
			http.Redirect(w, r, "https://example.com/login", 302)
			return
		}
		if r.URL.Path == "/services/jobs/options/facetValues/" {
			var body map[string]any
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body["keywords"] != "" || len(body["filterquery"].(map[string]any)) != 0 {
				t.Error("public facet request changed")
			}
			stage := "Graduate"
			if *scenario == "facet_changed" {
				stage = "Student"
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"facets":{"map":{"country":[{"name":"CN"}],"customfield3":[{"name":%q}]}}}`, stage)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Path == "/search/" {
			q := r.URL.Query()
			offset, err := strconv.Atoi(q.Get("startrow"))
			if r.Method != "GET" || err != nil || offset < 0 || offset%25 != 0 || q.Get("q") != "" || q.Get("optionsFacetsDD_country") != "CN" || q.Get("optionsFacetsDD_customfield3") != "Graduate" || len(q) != 4 {
				t.Error("campus query scope changed")
			}
			if *scenario == "empty" || *scenario == "empty_wrong_scope" || *scenario == "recommendations" {
				label := "Graduate AND China"
				if *scenario == "empty_wrong_scope" {
					label = "Student AND China"
				}
				fmt.Fprintf(w, `<div id="noresults"><span class="securitySearchString">%s</span></div>`, label)
				if *scenario == "recommendations" {
					fmt.Fprint(w, `<table id="searchresults"><tr class="data-row"></tr></table>`)
				}
				return
			}
			total := 52
			if *scenario == "capacity" {
				total = 501
			}
			if *scenario == "changed_total" && offset == 25 {
				total++
			}
			page, scope := offset/25+1, "Graduate AND China"
			if *scenario == "mixed_scope" {
				scope = "Professional AND China"
			}
			pageLabel := page
			if *scenario == "wrong_page" && page == 2 {
				pageLabel = 1
			}
			fmt.Fprintf(w, `<table id="searchresults" aria-label="Search results for %s. Page %d of %d, Results %d to %d of %d">`, scope, pageLabel, (total+24)/25, offset+1, min(offset+25, total), total)
			end := min(offset+25, total)
			if *scenario == "truncated" && page == 2 {
				end--
			}
			for n := offset + 1; n <= end; n++ {
				id, title, city := n, "Go developer "+strconv.Itoa(n), "Shanghai, CN, 201203"
				if *scenario == "duplicate" && n == 26 {
					id = 1
				}
				if *scenario == "foreign" && n == 1 {
					city = "Doha, QA, 26660"
				}
				mobileTitle := title
				if *scenario == "mobile_mismatch" && n == 1 {
					mobileTitle = "other job"
				}
				fmt.Fprintf(w, `<tr class="data-row"><td><a class="jobTitle-link" href="/job/Shanghai-Go/%d/">%s</a><a class="jobTitle-link" href="/job/Shanghai-Go/%d/">%s</a></td><td class="colLocation"><span>%s</span></td></tr>`, id, title, id, mobileTitle, city)
			}
			fmt.Fprint(w, `</table>`)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/job/Shanghai-Go/") {
			id := strings.Split(r.URL.Path, "/")[3]
			canonical, title, stage, city, org, desc := sapOrigin+r.URL.Path, "Go developer "+id, "Graduate", "Shanghai, CN, 201203", "SAP", `<p>Build Go services with goroutines.</p><ul><li>Go or C++ required.</li><li>Redis is a plus.</li></ul>`
			switch *scenario {
			case "wrong_id":
				canonical = sapOrigin + "/job/Shanghai-Go/999/"
			case "wrong_title":
				title = "other job"
			case "intern":
				stage = "Student"
			case "professional":
				stage = "Professional"
			case "foreign_detail":
				city = "Doha, QA, 26660"
			case "changed_city":
				city = "Beijing, CN, 100016"
			case "wrong_org":
				org = "Other company"
			case "no_description":
				desc = `<p> </p>`
			}
			fmt.Fprintf(w, `<link rel="canonical" href="%s"><div class="jobDisplayShell" itemtype="http://schema.org/JobPosting"><meta itemprop="hiringOrganization" content="%s"><meta itemprop="streetAddress" content="%s"><span data-careersite-propertyid="title">%s</span><span data-careersite-propertyid="customfield3">%s</span><span data-careersite-propertyid="shifttype">Regular Full Time</span><span data-careersite-propertyid="description">%s</span></div><footer>Do not import this unrelated footer.</footer>`, canonical, org, city, title, stage, desc)
			return
		}
		t.Errorf("unexpected source request %s", r.URL.String())
	}))
}

func TestSAPScopedPaginationAndOriginal(t *testing.T) {
	scenario := ""
	srv := sapFixture(t, &scenario)
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(SAPCampusURL)
	jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "private"}})
	calls := 0
	a := PublicPlatform{Client: &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}, Allow: func(_ context.Context, key string, limit int) (bool, error) {
		if key != "sap:public-site" || limit != 30 {
			t.Error("missing shared source quota")
		}
		calls++
		return true, nil
	}}
	s := d.Source{ID: d.ID(), Adapter: "sap", Tenant: sapScope, RateLimit: 30}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil || len(refs) != 52 || calls != 4 || refs[51].ExternalID != "52" || refs[0].Locations[0] != "上海" || refs[0].JobType != "UNKNOWN" {
		t.Fatalf("full scan %d, requests %d: %v", len(refs), calls, err)
	}
	for _, ref := range refs {
		if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []PostingRef{refs[0], refs[51]} {
		res, err := a.FetchPosting(context.Background(), s, ref)
		if err != nil || !strings.Contains(res.Text, "Regular Full Time") || !strings.Contains(res.Text, "Redis is a plus.") || strings.Contains(res.Text, "unrelated footer") || strings.Contains(res.Text, "<p>") {
			t.Fatalf("original %+v %v", res, err)
		}
	}
	for _, sc := range []string{"wrong_id", "wrong_title", "intern", "professional", "foreign_detail", "changed_city", "wrong_org", "no_description"} {
		scenario = sc
		if _, err := a.FetchPosting(context.Background(), s, refs[0]); err == nil {
			t.Errorf("accepted wrong original: %s", sc)
		}
	}
	for _, sc := range []string{"capacity", "facet_changed", "changed_total", "mixed_scope", "wrong_page", "truncated", "duplicate", "foreign", "mobile_mismatch", "empty_wrong_scope", "recommendations", "blocked", "redirect"} {
		scenario = sc
		if refs, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Keyword: "not found"}}); err == nil || len(refs) != 0 {
			t.Errorf("accepted incomplete scope: %s", sc)
		}
	}
	scenario = "empty"
	v, err := a.PreviewCampus(context.Background(), SAPCampusURL)
	if err != nil || v.Total != 0 || v.Adapter != "sap" || v.ProjectCode != sapScope || v.MinimumInterval != 1800 {
		t.Fatalf("empty scope %+v %v", v, err)
	}
	before := calls
	s.Tenant = "CN_Student"
	if _, err := a.Discover(context.Background(), s, d.WatchTarget{}); err == nil || calls != before {
		t.Fatal("unknown scope reached network")
	}
	s.Tenant = sapScope
	if _, err := a.Discover(context.Background(), s, d.WatchTarget{WatchInput: d.WatchInput{Direction: "rd"}}); err == nil || calls != before {
		t.Fatal("unsupported direction reached network")
	}
	for _, raw := range []string{strings.Replace(SAPCampusURL, "CN", "US", 1), strings.Replace(SAPCampusURL, "Graduate", "Student", 1), SAPCampusURL + "&startrow=25", SAPCampusURL + "&optionsFacetsDD_country=US", SAPCampusURL + "#intern", "https://jobs.sap.com/"} {
		if RecognizeCampusURL(raw) == nil {
			t.Errorf("accepted unsupported entry %s", raw)
		}
	}
	for _, raw := range []string{"https://user:secret@careers.sap.com/job/Shanghai-Go/1/", "https://example.com/job/Shanghai-Go/1/", "https://careers.sap.com/job/Shanghai-Go%2Fevil/1/", "https://careers.sap.com/job/Shanghai-Go/1/?next=private"} {
		if _, err := sapJobID(raw); err == nil {
			t.Errorf("accepted unsupported job URL %s", raw)
		}
	}
}

func TestSAPCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_SOURCES") != "1" {
		t.Skip("explicit public read-only verification")
	}
	a := PublicPlatform{}
	s := d.Source{ID: "sap-live", Adapter: "sap", Tenant: sapScope}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if len(refs) > 0 {
		for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			res, err := a.FetchPosting(ctx, s, ref)
			cancel()
			if err != nil || res.Status != "SUCCESS" {
				t.Fatalf("original %s: %v", ref.ExternalID, err)
			}
			t.Logf("original %s %s (%d bytes)", ref.ExternalID, ref.Title, len(res.Text))
		}
	}
	t.Logf("complete China Graduate scope: %d postings", len(refs))
}
