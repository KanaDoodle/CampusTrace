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

func ctripFixture(t *testing.T, scenario string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/hrrecruit/getJobAd" || r.Method != http.MethodPost || r.Header.Get("Referer") != CtripCampusURL || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("invalid public request: %s %s", r.Method, r.URL)
		}
		if scenario == "blocked" {
			w.WriteHeader(403)
			return
		}
		if scenario == "verify_redirect" {
			http.Redirect(w, r, "https://verify.ctrip.com/static/ctripVerify.html", http.StatusFound)
			return
		}
		var body struct {
			Condition struct {
				FromID   []string `json:"fromId"`
				Category *int     `json:"category"`
				Kind     []string `json:"kind"`
			} `json:"condition"`
			Pager struct {
				Index string `json:"index"`
				Size  string `json:"size"`
			} `json:"pager"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Pager.Size != "10" {
			t.Error("unexpected page size")
		}
		job := func(n int) map[string]any {
			kind, id := "1", fmt.Sprintf("MJ%06d", n)
			if scenario == "intern" && n == 1 {
				kind = "2"
			}
			if scenario == "duplicate" && n == 11 {
				id = "MJ000001"
			}
			if scenario == "wrong_detail" && len(body.Condition.FromID) > 0 {
				id = "MJ999999"
			}
			return map[string]any{"fromId": id, "jobTitle": "Go 开发 " + strconv.Itoa(n), "kind": kind, "cityName": "上海", "requirements": "<p>职责：开发服务</p><p>要求：Go；Redis 加分</p>"}
		}
		rows := []any{}
		total := 12
		if scenario == "capacity" {
			total = 501
		}
		if len(body.Condition.FromID) > 0 {
			if body.Condition.Category != nil || len(body.Condition.FromID) != 1 {
				t.Error("detail filter changed")
			}
			n, _ := strconv.Atoi(strings.TrimPrefix(body.Condition.FromID[0], "MJ"))
			total = 1
			rows = append(rows, job(n))
		} else {
			if body.Condition.Category == nil || *body.Condition.Category != 2 || body.Condition.Kind == nil || len(body.Condition.Kind) != 0 || body.Pager.Index == "" {
				t.Error("campus filter changed")
			}
			page, _ := strconv.Atoi(body.Pager.Index)
			for n := (page-1)*10 + 1; n <= min(page*10, 12); n++ {
				rows = append(rows, job(n))
			}
			if scenario == "truncated" && page == 2 {
				rows = rows[:1]
			}
		}
		v := map[string]any{"retCode": "201", "retValue": map[string]any{"total": total, "recruitJobAdList": rows}}
		if scenario == "schema" {
			delete(v, "retCode")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}))
}

func TestCtripCampusFullScopeAndDetails(t *testing.T) {
	srv := ctripFixture(t, "")
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(CtripCampusURL)
	jar.SetCookies(u, []*http.Cookie{{Name: "candidate", Value: "secret"}})
	calls := 0
	a := PublicPlatform{Client: &http.Client{Jar: jar, Transport: rewriteTransport{srv.URL}}, Allow: func(_ context.Context, key string, limit int) (bool, error) {
		if key != "ctrip:public-site" || limit != 30 {
			t.Error("rate limit scope changed")
		}
		calls++
		return true, nil
	}}
	s := d.Source{ID: d.ID(), Adapter: "ctrip", Tenant: "campus", RateLimit: 30}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil || len(refs) != 12 || refs[0].ExternalID != "MJ000001" || refs[11].ExternalID != "MJ000012" {
		t.Fatalf("discover %d %v", len(refs), err)
	}
	for _, ref := range refs {
		if ref.JobType != "FULL_TIME" || ref.Company != "携程/Trip.com" || ref.URL != ctripURL(ref.ExternalID) {
			t.Fatal(ref)
		}
		if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: "public original", FetchStatus: "SUCCESS"}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
		result, err := a.FetchPosting(context.Background(), s, ref)
		if err != nil || result.Status != "SUCCESS" || !strings.Contains(result.Text, "Redis 加分") || strings.Contains(result.Text, "<p>") {
			t.Fatalf("detail %+v %v", result, err)
		}
	}
	if calls < 4 {
		t.Fatal("missing pacing")
	}
	for _, raw := range []string{CtripCampusURL, "https://careers.ctrip.com/campus/jobList?kind=2", "https://careers.ctrip.com/", "https://careers.ctrip.com.evil.test/campus/jobList"} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("accepted unscoped URL", raw)
		}
	}
}

func TestCtripCampusRejectsInvalidScansAndDetails(t *testing.T) {
	for _, scenario := range []string{"intern", "duplicate", "truncated", "capacity", "schema", "blocked", "verify_redirect"} {
		t.Run(scenario, func(t *testing.T) {
			srv := ctripFixture(t, scenario)
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			refs, err := a.Discover(context.Background(), d.Source{ID: d.ID(), Adapter: "ctrip", Tenant: "campus"}, d.WatchTarget{})
			if err == nil || len(refs) != 0 {
				t.Fatalf("accepted partial or wrong scope: %d %v", len(refs), err)
			}
			if scenario == "verify_redirect" {
				var fetch *FetchError
				if !AsFetchError(err, &fetch) || fetch.Category != "BLOCKED" {
					t.Fatalf("verification redirect misclassified: %v", err)
				}
			}
		})
	}
	srv := ctripFixture(t, "wrong_detail")
	defer srv.Close()
	a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
	s := d.Source{ID: d.ID(), Adapter: "ctrip", Tenant: "campus"}
	refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.FetchPosting(context.Background(), s, refs[0])
	if err == nil || result.Status == "SUCCESS" {
		t.Fatal("mismatched detail accepted")
	}
}

func TestCtripCampusLiveReadOnly(t *testing.T) {
	if os.Getenv("CAMPUS_LIVE_CTRIP") != "1" {
		t.Skip("explicit public read-only verification")
	}
	a := PublicPlatform{}
	s := d.Source{ID: "ctrip-live", Adapter: "ctrip", Tenant: "campus", RateLimit: 30}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	refs, err := a.Discover(ctx, s, d.WatchTarget{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("empty live scope")
	}
	for _, ref := range []PostingRef{refs[0], refs[len(refs)-1]} {
		result, err := a.FetchPosting(ctx, s, ref)
		if err != nil || result.Status != "SUCCESS" {
			t.Fatalf("detail %s: %+v %v", ref.ExternalID, result, err)
		}
		if err := (p.Ingest{SourceID: s.ID, ExternalID: ref.ExternalID, URL: ref.URL, Title: ref.Title, Company: ref.Company, JobType: ref.JobType, Locations: ref.Locations, Text: result.Text, FetchStatus: result.Status}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("live full scope %d; first %s; last %s", len(refs), refs[0].ExternalID, refs[len(refs)-1].ExternalID)
}
