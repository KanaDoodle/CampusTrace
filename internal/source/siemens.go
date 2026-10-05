package source

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"golang.org/x/net/html"
)

const siemensOrigin = "https://jobs.siemens.com.cn"
const siemensScope = "CAMPUSRECRUITMENT"
const siemensPageSize = 15

var siemensID = regexp.MustCompile(`^[a-f0-9]{24}$`)
var siemensCount = regexp.MustCompile(`^共([0-9]+)个职位$`)
var siemensPages = regexp.MustCompile(`^共([0-9]+)页$`)
var siemensDetailScope = regexp.MustCompile(`staticParams\.detailRecruitmentType\s*=\s*["']([A-Z]+)["']`)

func siemensDetailURL(id string) string {
	return siemensOrigin + "/siemens/position/detail?positionId=" + id + "&recruitmentType=" + siemensScope + "&currentLang=zh_CN"
}
func (a PublicPlatform) siemensPage(ctx context.Context, s d.Source, page int) ([]PostingRef, int, error) {
	root, err := a.document(ctx, s, siemensOrigin+"/siemens/position/nextPageList", url.Values{"recruitmentType": {siemensScope}, "offset": {strconv.Itoa((page - 1) * siemensPageSize)}, "max": {strconv.Itoa(siemensPageSize)}})
	if err != nil {
		return nil, 0, err
	}
	header, err := uniqueNode(root, func(n *html.Node) bool { return nodeClass(n, "card-header") })
	if err != nil {
		return nil, 0, err
	}
	countNode, err := uniqueNode(header, func(n *html.Node) bool { return nodeClass(n, "txt") })
	if err != nil {
		return nil, 0, err
	}
	count := siemensCount.FindStringSubmatch(nodeText(countNode))
	if len(count) != 2 {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	total, err := strconv.Atoi(count[1])
	if err != nil {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	if total > MaxPostings {
		return nil, 0, fail("CAPACITY", false, 200)
	}
	if total > 0 {
		pagination, err := uniqueNode(root, func(n *html.Node) bool { return nodeAttr(n, "id") == "pageTurnUl" })
		if err != nil {
			return nil, 0, err
		}
		active, err := uniqueNode(pagination, func(n *html.Node) bool { return nodeClass(n, "page-item") && nodeClass(n, "active") })
		if err != nil || nodeText(active) != strconv.Itoa(page) {
			return nil, 0, fail("SCHEMA_INVALID", false, 200)
		}
		pages, err := uniqueNode(pagination, func(n *html.Node) bool { return nodeClass(n, "page-item") && siemensPages.MatchString(nodeText(n)) })
		if err != nil || nodeText(pages) != fmt.Sprintf("共%d页", (total+siemensPageSize-1)/siemensPageSize) {
			return nil, 0, fail("SCHEMA_INVALID", false, 200)
		}
		for _, n := range nodes(pagination, func(n *html.Node) bool { return nodeAttr(n, "data-action") == "pageTurn" }) {
			p, e := strconv.Atoi(nodeAttr(n, "data-pagenum"))
			if e != nil || p < 1 || p > (total+siemensPageSize-1)/siemensPageSize || nodeAttr(n, "data-pagemax") != strconv.Itoa(siemensPageSize) {
				return nil, 0, fail("SCHEMA_INVALID", false, 200)
			}
		}
	}
	rows := nodes(root, func(n *html.Node) bool { return nodeAttr(n, "data-action") == "positionItem" })
	if len(rows) != min(siemensPageSize, max(0, total-(page-1)*siemensPageSize)) {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	refs := []PostingRef{}
	for _, row := range rows {
		id := nodeAttr(row, "pid")
		if !siemensID.MatchString(id) || nodeAttr(row, "recruitment") != siemensScope {
			return nil, 0, fail("SCHEMA_INVALID", false, 200)
		}
		name, err := uniqueNode(row, func(n *html.Node) bool { return nodeClass(n, "position-name") })
		if err != nil {
			return nil, 0, err
		}
		title, err := uniqueNode(name, func(n *html.Node) bool { return nodeClass(n, "txt") })
		if err != nil {
			return nil, 0, err
		}
		locations := []string{}
		for _, li := range nodes(row, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == "li" }) {
			if value, ok := strings.CutPrefix(nodeText(li), "工作地点："); ok {
				locations = append(locations, splitXHSLocations(value)...)
			}
		}
		ref := PostingRef{ExternalID: id, URL: siemensDetailURL(id), Title: nodeText(title), Company: "西门子", JobType: "FULL_TIME", Locations: locations}
		if err := validateRef(ref); err != nil {
			return nil, 0, err
		}
		refs = append(refs, ref)
	}
	return refs, total, nil
}
func (a PublicPlatform) previewSiemens(ctx context.Context, site CampusSite) (CampusPreview, error) {
	s := d.Source{ID: "siemens-preview", Adapter: "siemens", Tenant: siemensScope, RateLimit: 30}
	a, err := a.anonymousOrigin(s, siemensOrigin)
	if err != nil {
		return CampusPreview{}, err
	}
	refs, total, err := a.siemensPage(ctx, s, 1)
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: s.Tenant, Total: total, Samples: refs[:min(5, len(refs))]}, err
}
func (a PublicPlatform) discoverSiemens(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	a, err := a.anonymousOrigin(s, siemensOrigin)
	if err != nil {
		return nil, err
	}
	refs, total, err := a.siemensPage(ctx, s, 1)
	if err != nil {
		return nil, err
	}
	for page := 2; page <= (total+siemensPageSize-1)/siemensPageSize; page++ {
		rows, count, err := a.siemensPage(ctx, s, page)
		if err != nil {
			return nil, err
		}
		if count != total {
			return nil, fail("SCHEMA_INVALID", false, 200)
		}
		refs = append(refs, rows...)
	}
	return refs, nil
}
func (a PublicPlatform) fetchSiemens(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !siemensID.MatchString(r.ExternalID) || r.URL != siemensDetailURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	a, err := a.anonymousOrigin(s, siemensOrigin)
	if err != nil {
		return "", err
	}
	root, err := a.document(ctx, s, r.URL, nil)
	if err != nil {
		return "", err
	}
	id, err := uniqueNode(root, func(n *html.Node) bool { return n.Data == "input" && nodeAttr(n, "id") == "positionId" })
	if err != nil || nodeAttr(id, "value") != r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	status, err := uniqueNode(root, func(n *html.Node) bool { return n.Data == "input" && nodeAttr(n, "id") == "positionStatus" })
	if err != nil || nodeAttr(status, "value") != "PUBLISHING" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	scopes := []string{}
	for _, script := range nodes(root, func(n *html.Node) bool { return n.Data == "script" }) {
		for c := script.FirstChild; c != nil; c = c.NextSibling {
			for _, match := range siemensDetailScope.FindAllStringSubmatch(c.Data, -1) {
				scopes = append(scopes, match[1])
			}
		}
	}
	if len(scopes) != 1 || scopes[0] != siemensScope {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	header, err := uniqueNode(root, func(n *html.Node) bool { return nodeClass(n, "position-header") })
	if err != nil {
		return "", err
	}
	name, err := uniqueNode(header, func(n *html.Node) bool { return n.Data == "h4" && nodeClass(n, "name") })
	if err != nil || nodeText(name) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	detail, err := uniqueNode(root, func(n *html.Node) bool { return nodeAttr(n, "id") == "positionDetail" })
	if err != nil {
		return "", err
	}
	description, err := uniqueNode(detail, func(n *html.Node) bool { return nodeClass(n, "position-description") })
	if err != nil || nodeText(description) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return "岗位名称：" + nodeText(name) + "\n招聘范围：西门子中国 · 官网校招分类\n岗位原文（含职责与要求）：\n" + nodeText(detail), nil
}
