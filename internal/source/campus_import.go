package source

import "context"

// ResolveCampusImport resolves a server-owned preset without scanning its list
// twice. Fixed projects use the same scopes as their existing adapters; XHS
// resolves its current regular project from the official enum endpoint.
func (a PublicPlatform) ResolveCampusImport(ctx context.Context, raw string) (CampusPreview, error) {
	site, err := campusSite(raw)
	if err != nil {
		return CampusPreview{}, err
	}
	if site.Adapter == "xiaohongshu" {
		v, err := a.PreviewXHS(ctx, raw)
		v.Adapter = site.Adapter
		v.Name = site.Company + " · " + v.Name
		return v, err
	}
	tenant := moreTenant(site.Adapter)
	if tenant == "" {
		tenant = map[string]string{"baidu": "GRADUATE", "meituan": "graduate", "jd": "present", "netease": "103", "alibaba": "100000760001", "bilibili": "freshmen", "siemens": siemensScope, "haier": "68", "oppo": "30", "kuaishou": kuaishouProjectCode}[site.Adapter]
	}
	if tenant == "" {
		return CampusPreview{}, fail("UNSUPPORTED", false, 0)
	}
	return CampusPreview{URL: site.URL, Adapter: site.Adapter, Name: site.Company + " · 校招", ProjectCode: tenant}, nil
}
