package source

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestBeisenRequestsPublishedLocationAndQualificationFields(t *testing.T) {
	for _, kind := range []string{"全职", "实习", ""} {
		t.Run("kind="+kind, func(t *testing.T) {
			id := "00000000-0000-0000-0000-000000000001"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fields := []string{}
				list := r.Method == http.MethodPost && r.URL.Path == "/api/Jobad/GetJobAdPageList"
				if list {
					var body struct {
						Category int      `json:"category"`
						Page     int      `json:"PageIndex"`
						Size     int      `json:"PageSize"`
						Fields   []string `json:"displayFields"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Category != 2 || body.Page != 0 || body.Size != 50 {
						t.Error("public campus query changed", err)
					}
					fields = body.Fields
				} else {
					if r.Method != http.MethodGet || r.URL.Path != "/api/JobAd/GetJobAdInfo" || r.URL.Query().Get("category") != "2" || r.URL.Query().Get("jobAdId") != id {
						t.Error("public detail binding changed")
					}
					if err := json.Unmarshal([]byte(r.URL.Query().Get("displayFields")), &fields); err != nil {
						t.Error(err)
					}
				}
				has := func(field string) bool {
					for _, f := range fields {
						if f == field {
							return true
						}
					}
					return false
				}
				// Simulate the public API: LocNames stays empty unless LocId was
				// requested. Returned labels, rather than a title or address guess,
				// are the source of location and qualification metadata.
				row := map[string]any{"Id": id, "JobAdId": 1, "JobAdName": "软件开发工程师（杭州）", "CategoryId": "2", "Status": 1, "LocNames": []string{}, "Duty": "开发 Go 服务", "Require": "Go 与并发编程"}
				if kind != "" {
					if has("LocId") {
						row["LocNames"] = []string{"浙江省·杭州市", "北京市"}
					}
					if has("Kind") {
						row["Kind"] = kind
					}
					if has("Degree") {
						row["Degree"] = "本科及以上"
					}
				}
				var data any = row
				if list {
					data = []any{row}
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(map[string]any{"Code": 200, "Count": 1, "Data": data}); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			a := PublicPlatform{Client: &http.Client{Transport: rewriteTransport{srv.URL}}}
			s := d.Source{Adapter: "h3c", Tenant: "campus"}
			refs, err := a.Discover(context.Background(), s, d.WatchTarget{})
			if err != nil || len(refs) != 1 {
				t.Fatal("list unavailable", err)
			}
			res, err := a.FetchPosting(context.Background(), s, refs[0])
			if err != nil || res.Status != "SUCCESS" {
				t.Fatal("detail unavailable", res, err)
			}
			if kind == "" {
				if refs[0].JobType != "UNKNOWN" || len(refs[0].Locations) != 0 || strings.Contains(res.Text, "官网学历要求") || strings.Contains(res.Text, "应届生全职") {
					t.Fatal("missing public facts guessed from title or campus category", refs[0], res.Text)
				}
			} else {
				wantType := "FULL_TIME"
				if kind == "实习" {
					wantType = "INTERNSHIP"
				}
				if refs[0].JobType != wantType || len(refs[0].Locations) != 2 || refs[0].Locations[0] != "浙江省·杭州市" || !strings.Contains(res.Text, "浙江省·杭州市") || !strings.Contains(res.Text, "官网工作性质："+kind) || !strings.Contains(res.Text, "官网学历要求：本科及以上") {
					t.Fatal("upstream selector lost city, employment or degree", refs[0], res.Text)
				}
			}
		})
	}
}

func TestH3CVerifiedAliasUsesCanonicalPreset(t *testing.T) {
	for _, raw := range []string{"https://career.h3c.com/campus/jobs", "https://h3c.zhiye.com/Campus", "https://h3c.zhiye.com/"} {
		site, err := campusSite(raw)
		if err != nil || site.Adapter != "h3c" || site.URL != "https://career.h3c.com/campus/jobs" {
			t.Fatal("official alias did not resolve to canonical preset", site, err)
		}
	}
	for _, raw := range []string{"https://career.h3c.com/social", "https://h3c.zhiye.com/Campus?candidate=private", "https://h3c.zhiye.com/Intern"} {
		if RecognizeCampusURL(raw) == nil {
			t.Fatal("foreign recruiting scope accepted", raw)
		}
	}
}
