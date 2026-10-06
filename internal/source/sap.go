package source

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"golang.org/x/net/html"
)

const sapOrigin = "https://careers.sap.com"
const sapScope = "CN_Graduate"
const SAPCampusURL = sapOrigin + "/search/?q=&optionsFacetsDD_country=CN&optionsFacetsDD_customfield3=Graduate"

var sapPageLabel = regexp.MustCompile(`^Search results for Graduate AND China\. Page ([0-9]+) of ([0-9]+), Results ([0-9]+) to ([0-9]+) of ([0-9]+)$`)

func sapSearchURL(page int) string {
	return SAPCampusURL + "&startrow=" + strconv.Itoa((page-1)*25)
}

// Graduate is the portal's career stage, not a promise of a particular
// graduation year or zero experience. Student and Professional are separate.
func (a PublicPlatform) verifySAP(ctx context.Context, s d.Source) error {
	var v struct {
		Facets struct {
			Map map[string][]struct {
				Name string `json:"name"`
			} `json:"map"`
		} `json:"facets"`
	}
	body := map[string]any{
		"page": 0, "keywords": "", "locationsearch": "", "sortby": "referencedate", "sortdir": "desc", "sortfield": "title", "recordsperpage": 25, "startrow": 0,
		"facetquery": map[string]any{"facet": true, "mincount": 1, "limit": 5000, "fields": []string{"customfield3", "country"}, "sort": "index", "showPicklistAllLocales": false}, "filterquery": map[string]any{},
	}
	if err := a.post(ctx, s, sapOrigin+"/services/jobs/options/facetValues/", body, &v); err != nil {
		return err
	}
	for field, want := range map[string]string{"customfield3": "Graduate", "country": "CN"} {
		count := 0
		for _, item := range v.Facets.Map[field] {
			if item.Name == want {
				count++
			}
		}
		if count != 1 {
			return fail("SCHEMA_INVALID", false, 200)
		}
	}
	return nil
}

func sapJobID(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "careers.sap.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || len(raw) > 2000 {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "job" || parts[2] == "" || parts[4] != "" || !numericID(parts[3]) || len(parts[3]) > 18 {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	return parts[3], nil
}

func sapCity(raw string) (string, error) {
	parts := strings.Split(raw, ",")
	if len(parts) < 2 || len(parts) > 3 || strings.TrimSpace(parts[1]) != "CN" || strings.TrimSpace(parts[0]) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	city := strings.TrimSpace(parts[0])
	if local := map[string]string{"Beijing": "北京", "Shanghai": "上海", "Dalian China": "大连", "Dalian": "大连", "Chengdu": "成都", "Shenzhen": "深圳", "Xi'an": "西安", "Nanjing": "南京", "Guangzhou": "广州", "Hangzhou": "杭州"}[city]; local != "" {
		city = local
	}
	return city, nil
}

func sapPage(root *html.Node, page int) (campusPage, error) {
	out := campusPage{Refs: []PostingRef{}, Page: page, Size: 25}
	tables := nodes(root, func(n *html.Node) bool { return n.Data == "table" && nodeAttr(n, "id") == "searchresults" })
	empty := nodes(root, func(n *html.Node) bool { return nodeAttr(n, "id") == "noresults" })
	if len(empty) > 0 {
		// Some SuccessFactors pages show unrelated recommendations below the
		// empty banner. Never ingest those as this scope's search results.
		labels := nodes(empty[0], func(n *html.Node) bool { return nodeClass(n, "securitySearchString") })
		if len(empty) != 1 || page != 1 || len(tables) != 0 || len(labels) != 1 || nodeText(labels[0]) != "Graduate AND China" {
			return out, fail("SCHEMA_INVALID", false, 200)
		}
		return out, nil
	}
	if len(tables) != 1 {
		return out, fail("SCHEMA_INVALID", false, 200)
	}
	label := sapPageLabel.FindStringSubmatch(nodeAttr(tables[0], "aria-label"))
	if len(label) != 6 {
		return out, fail("SCHEMA_INVALID", false, 200)
	}
	values := make([]int, 5)
	for i := range values {
		var err error
		values[i], err = strconv.Atoi(label[i+1])
		if err != nil {
			return out, fail("SCHEMA_INVALID", false, 200)
		}
	}
	out.Total = values[4]
	if out.Total > MaxPostings {
		return out, fail("CAPACITY", false, 200)
	}
	if values[0] != page || out.Total <= 0 || values[1] != (out.Total+24)/25 || values[2] != (page-1)*25+1 || values[3] != min(page*25, out.Total) {
		return out, fail("SCHEMA_INVALID", false, 200)
	}
	for _, row := range nodes(tables[0], func(n *html.Node) bool { return n.Data == "tr" && nodeClass(n, "data-row") }) {
		// The official HTML repeats each title on desktop and mobile. Both
		// copies must agree; one row is still exactly one original posting.
		links := nodes(row, func(n *html.Node) bool { return n.Data == "a" && nodeClass(n, "jobTitle-link") })
		if len(links) != 2 || nodeAttr(links[0], "href") != nodeAttr(links[1], "href") || nodeText(links[0]) != nodeText(links[1]) {
			return out, fail("SCHEMA_INVALID", false, 200)
		}
		href := nodeAttr(links[0], "href")
		if !strings.HasPrefix(href, "/job/") {
			return out, fail("SCHEMA_INVALID", false, 200)
		}
		raw := sapOrigin + href
		id, err := sapJobID(raw)
		if err != nil {
			return out, err
		}
		location, err := uniqueNode(row, func(n *html.Node) bool { return n.Data == "td" && nodeClass(n, "colLocation") })
		if err != nil {
			return out, err
		}
		city, err := sapCity(nodeText(location))
		if err != nil {
			return out, err
		}
		out.Refs = append(out.Refs, PostingRef{ExternalID: id, URL: raw, Title: nodeText(links[0]), Company: "SAP", JobType: "UNKNOWN", Locations: []string{city}})
	}
	return out, nil
}

func (a PublicPlatform) discoverSAP(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.verifySAP(ctx, s); err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		root, err := a.document(ctx, s, sapSearchURL(page), nil)
		if err != nil {
			return campusPage{}, err
		}
		return sapPage(root, page)
	})
}

func sapOriginal(root *html.Node, ref PostingRef) (string, error) {
	canonical, err := uniqueNode(root, func(n *html.Node) bool { return n.Data == "link" && nodeAttr(n, "rel") == "canonical" })
	if err != nil || nodeAttr(canonical, "href") != ref.URL {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	shell, err := uniqueNode(root, func(n *html.Node) bool {
		return nodeClass(n, "jobDisplayShell") && nodeAttr(n, "itemtype") == "http://schema.org/JobPosting"
	})
	if err != nil {
		return "", err
	}
	property := func(key string) (string, error) {
		n, err := uniqueNode(shell, func(n *html.Node) bool { return nodeAttr(n, "data-careersite-propertyid") == key })
		if err != nil {
			return "", err
		}
		return nodeText(n), nil
	}
	title, e1 := property("title")
	stage, e2 := property("customfield3")
	employment, e3 := property("shifttype")
	description, e4 := property("description")
	org, e5 := uniqueNode(shell, func(n *html.Node) bool { return n.Data == "meta" && nodeAttr(n, "itemprop") == "hiringOrganization" })
	address, e6 := uniqueNode(shell, func(n *html.Node) bool { return n.Data == "meta" && nodeAttr(n, "itemprop") == "streetAddress" })
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil || e6 != nil || title != ref.Title || stage != "Graduate" || employment == "" || description == "" || nodeAttr(org, "content") != "SAP" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	city, err := sapCity(nodeAttr(address, "content"))
	if err != nil || len(ref.Locations) != 1 || city != ref.Locations[0] {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return "岗位名称：" + title + "\n公司：SAP\n官网职业阶段：Graduate（具体届别及经验要求见原文）\n工作地点：" + nodeAttr(address, "content") + "\n官网用工类型：" + employment + "\n岗位原文：\n" + description, nil
}

func (a PublicPlatform) fetchSAP(ctx context.Context, s d.Source, ref PostingRef) (string, error) {
	id, err := sapJobID(ref.URL)
	if err != nil || id != ref.ExternalID || ref.Company != "SAP" || validateRef(ref) != nil {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	if err := a.verifySAP(ctx, s); err != nil {
		return "", err
	}
	root, err := a.document(ctx, s, ref.URL, nil)
	if err != nil {
		return "", err
	}
	return sapOriginal(root, ref)
}
