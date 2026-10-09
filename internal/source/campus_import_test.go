package source

import (
	"context"
	"net/http"
	"testing"
)

type noImportNetwork struct{ t *testing.T }

func (n noImportNetwork) RoundTrip(*http.Request) (*http.Response, error) {
	n.t.Fatal("fixed preset unnecessarily scanned before import")
	return nil, nil
}
func TestCampusImportFixedScopesDoNotScanTwice(t *testing.T) {
	a := PublicPlatform{Client: &http.Client{Transport: noImportNetwork{t}}}
	for _, site := range CampusSites() {
		if site.Adapter == "xiaohongshu" {
			continue
		}
		v, err := a.ResolveCampusImport(context.Background(), site.URL)
		if err != nil || v.Adapter != site.Adapter || v.ProjectCode == "" || v.Name != site.Company+" · 校招" || len(v.Name) > 160 {
			t.Fatalf("%s: %+v %v", site.Adapter, v, err)
		}
	}
	for _, raw := range []string{"https://127.0.0.1/jobs", "https://example.com/jobs", BaiduCampusURL + "&recruitType=SOCIAL"} {
		if _, err := a.ResolveCampusImport(context.Background(), raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
