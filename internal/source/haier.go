package source

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"golang.org/x/net/html"
)

const haierOrigin = "https://maker.haier.net"
const haierProjectCode = "68"
const haierPageSize = 50

type haierPost struct {
	ID       string `json:"id"`
	Function string `json:"function_id"`
	Name     string `json:"name"`
	Location string `json:"addr"`
	URL      string `json:"click_url"`
}

func positiveID(id string) bool {
	v, err := strconv.ParseInt(id, 10, 64)
	return err == nil && v > 0 && strconv.FormatInt(v, 10) == id
}
func haierDetailPath(function, id string) string {
	return "/client/campusmobile/deliverfirst/id/68/fid/" + function + "/rid/" + id + ".html"
}
func (a PublicPlatform) haierScope(ctx context.Context, s d.Source) error {
	root, err := a.document(ctx, s, HaierCampusURL, nil)
	if err != nil {
		return err
	}
	name, err := uniqueNode(root, func(n *html.Node) bool { return nodeClass(n, "activity_name") })
	if err != nil || nodeText(name) != "海尔集团2027校园招聘" {
		return fail("SCHEMA_INVALID", false, 200)
	}
	links := nodes(root, func(n *html.Node) bool {
		return n.Data == "a" && strings.HasPrefix(nodeAttr(n, "href"), "/client/campusmobile/researchlist/")
	})
	if len(links) == 0 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	for _, link := range links {
		path := nodeAttr(link, "href")
		function := strings.TrimSuffix(strings.TrimPrefix(path, "/client/campusmobile/researchlist/id/68/fid/"), ".html")
		if !positiveID(function) || path != "/client/campusmobile/researchlist/id/68/fid/"+function+".html" {
			return fail("SCHEMA_INVALID", false, 200)
		}
	}
	return nil
}
func (a PublicPlatform) haierPage(ctx context.Context, s d.Source, page int) ([]PostingRef, bool, error) {
	var v struct {
		Status *int `json:"status"`
		Data   struct {
			Terminal *int        `json:"maxPage"`
			Stopped  *bool       `json:"activity_stop"`
			Rows     []haierPost `json:"list"`
		} `json:"data"`
	}
	input := url.Values{"id": {haierProjectCode}, "fid": {""}, "page": {strconv.Itoa(page)}, "pagesize": {strconv.Itoa(haierPageSize)}, "pagesource": {"0"}, "customized": {""}, "khaos": {""}, "ai_recommend": {""}, "place": {""}, "keyword": {""}}
	if err := a.post(ctx, s, haierOrigin+"/client/campusmobile/researchlist.html", input, &v); err != nil {
		return nil, false, err
	}
	if v.Status == nil || *v.Status != 1 || v.Data.Terminal == nil || (*v.Data.Terminal != 0 && *v.Data.Terminal != 1) || v.Data.Stopped == nil || *v.Data.Stopped || v.Data.Rows == nil || len(v.Data.Rows) > haierPageSize {
		return nil, false, fail("SCHEMA_INVALID", false, 200)
	}
	terminal := *v.Data.Terminal == 1
	if !terminal && len(v.Data.Rows) != haierPageSize {
		return nil, false, fail("SCHEMA_INVALID", false, 200)
	}
	refs := []PostingRef{}
	for _, row := range v.Data.Rows {
		if !positiveID(row.ID) || !positiveID(row.Function) || row.URL != haierDetailPath(row.Function, row.ID) {
			return nil, false, fail("SCHEMA_INVALID", false, 200)
		}
		ref := PostingRef{ExternalID: row.ID, URL: haierOrigin + row.URL, Title: row.Name, Company: "海尔集团", JobType: "FULL_TIME", Locations: splitXHSLocations(row.Location)}
		if err := validateRef(ref); err != nil {
			return nil, false, err
		}
		refs = append(refs, ref)
	}
	return refs, terminal, nil
}
func (a PublicPlatform) haierAll(ctx context.Context, s d.Source) ([]PostingRef, error) {
	a, err := a.anonymousOrigin(s, haierOrigin)
	if err != nil {
		return nil, err
	}
	if err := a.haierScope(ctx, s); err != nil {
		return nil, err
	}
	refs := []PostingRef{}
	seen := map[string]bool{}
	// The official response has no total; maxPage=1 is its end-of-list flag.
	// Preview walks to that flag too, so it never presents one page as all jobs.
	for page := 1; page <= MaxPostings/haierPageSize+1; page++ {
		rows, terminal, err := a.haierPage(ctx, s, page)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if seen[row.ExternalID] {
				return nil, fail("SCHEMA_DUPLICATE_ID", false, 200)
			}
			seen[row.ExternalID] = true
		}
		refs = append(refs, rows...)
		if len(refs) > MaxPostings {
			return nil, fail("CAPACITY", false, 200)
		}
		if terminal {
			return refs, nil
		}
	}
	return nil, fail("CAPACITY", false, 200)
}
func (a PublicPlatform) previewHaier(ctx context.Context, site CampusSite) (CampusPreview, error) {
	s := d.Source{ID: "haier-preview", Adapter: "haier", Tenant: haierProjectCode, RateLimit: 30}
	refs, err := a.haierAll(ctx, s)
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: s.Tenant, Total: len(refs), Samples: refs[:min(5, len(refs))]}, err
}
func (a PublicPlatform) discoverHaier(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	return a.haierAll(ctx, s)
}
func (a PublicPlatform) fetchHaier(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	u, err := url.Parse(r.URL)
	if err != nil || u.Scheme != "https" || u.Host != "maker.haier.net" || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.Fragment != "" || !positiveID(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	prefix := "/client/campusmobile/deliverfirst/id/68/fid/"
	function, tail, ok := strings.Cut(strings.TrimPrefix(u.Path, prefix), "/rid/")
	if !ok || !positiveID(function) || tail != r.ExternalID+".html" || u.Path != haierDetailPath(function, r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	a, err = a.anonymousOrigin(s, haierOrigin)
	if err != nil {
		return "", err
	}
	if err := a.haierScope(ctx, s); err != nil {
		return "", err
	}
	root, err := a.document(ctx, s, r.URL, nil)
	if err != nil {
		return "", err
	}
	identity, err := uniqueNode(root, func(n *html.Node) bool { return nodeAttr(n, "id") == "collect_btn" })
	if err != nil || nodeAttr(identity, "data-rid") != r.ExternalID || nodeAttr(identity, "data-aid") != haierProjectCode {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	name, err := uniqueNode(identity.Parent, func(n *html.Node) bool { return nodeClass(n, "span") })
	if err != nil || nodeText(name) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	detail, err := uniqueNode(root, func(n *html.Node) bool { return nodeAttr(n, "id") == "details_box" })
	if err != nil {
		return "", err
	}
	sections := map[string]string{}
	for _, item := range nodes(detail, func(n *html.Node) bool { return nodeClass(n, "item_div") }) {
		title, err := uniqueNode(item, func(n *html.Node) bool { return nodeClass(n, "title") })
		if err != nil {
			return "", err
		}
		text, err := uniqueNode(item, func(n *html.Node) bool { return nodeClass(n, "text") })
		if err != nil {
			return "", err
		}
		label := nodeText(title)
		if _, exists := sections[label]; exists {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		sections[label] = nodeText(text)
	}
	if sections["岗位描述"] == "" || sections["岗位要求"] == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	// Preserve departments separately from the group name; they do not prove
	// which subsidiary signs an employment contract.
	departments := []string{}
	for _, node := range nodes(root, func(n *html.Node) bool { return nodeClass(n, "department_box") }) {
		departments = append(departments, nodeText(node))
	}
	return "岗位名称：" + nodeText(name) + "\n招聘范围：海尔集团2027校园招聘\n岗位职责：\n" + sections["岗位描述"] + "\n任职要求：\n" + sections["岗位要求"] + "\n工作地点：" + sections["工作地点"] + "\n官网公开招聘部门：\n" + strings.Join(departments, "\n"), nil
}
