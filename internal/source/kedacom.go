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

const kedacomOrigin = "https://kedacom.zhiye.com"
const KedacomCampusURL = kedacomOrigin + "/campus/?r=2"

var legacyBeisenCount = regexp.MustCompile(`^共([0-9]+)条记录$`)
var legacyBeisenPage = regexp.MustCompile(`当前第([0-9]+)/([0-9]+)页`)

func kedacomJobURL(id string) string { return kedacomOrigin + "/zpdetail/" + id }

// This older Beisen site renders HTML. Its unfiltered campus page includes
// category 3 internships, so both the list query and detail category are checked.
func (a PublicPlatform) discoverKedacom(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		root, err := a.document(ctx, s, KedacomCampusURL+"&PageIndex="+strconv.Itoa(page), nil)
		if err != nil {
			return campusPage{}, err
		}
		return kedacomPage(root, page)
	})
}

func kedacomPage(root *html.Node, page int) (campusPage, error) {
	invalid := func() (campusPage, error) { return campusPage{}, fail("SCHEMA_INVALID", false, 200) }
	table, err := uniqueNode(root, func(n *html.Node) bool { return n.Data == "table" && nodeClass(n, "jobsTable") })
	if err != nil {
		return campusPage{}, err
	}
	count, err := uniqueNode(root, func(n *html.Node) bool { return nodeClass(n, "counts") })
	if err != nil {
		return campusPage{}, err
	}
	footer, err := uniqueNode(root, func(n *html.Node) bool { return nodeClass(n, "tablefooter") })
	if err != nil {
		return campusPage{}, err
	}
	c, p := legacyBeisenCount.FindStringSubmatch(nodeText(count)), legacyBeisenPage.FindStringSubmatch(nodeText(footer))
	if c == nil || p == nil {
		return invalid()
	}
	total, _ := strconv.Atoi(c[1])
	current, _ := strconv.Atoi(p[1])
	pages, _ := strconv.Atoi(p[2])
	if current != page || pages != max(1, (total+14)/15) {
		return invalid()
	}
	out := campusPage{Refs: []PostingRef{}, Total: total, Page: current, Size: 15}
	rows := nodes(table, func(n *html.Node) bool { return n.Data == "tr" })
	if len(rows) == 0 {
		return invalid()
	}
	for i, row := range rows {
		cells := nodes(row, func(n *html.Node) bool { return n.Data == "td" })
		if len(cells) != 4 {
			return invalid()
		}
		if i == 0 {
			if !nodeClass(row, "title") || nodeText(cells[0]) != "职位名称" || nodeText(cells[2]) != "工作地点" {
				return invalid()
			}
			continue
		}
		link, err := uniqueNode(cells[0], func(n *html.Node) bool { return n.Data == "a" })
		if err != nil {
			return invalid()
		}
		u, err := url.Parse(nodeAttr(link, "href"))
		if err != nil || u.IsAbs() || u.Host != "" || u.RawPath != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/zpdetail/") {
			return invalid()
		}
		id := strings.TrimPrefix(u.Path, "/zpdetail/")
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || !numericID(id) || len(q) != 2 || len(q["r"]) != 1 || q.Get("r") != "2" || len(q["PageIndex"]) != 1 || q.Get("PageIndex") != strconv.Itoa(page) {
			return invalid()
		}
		title := strings.TrimSpace(nodeAttr(link, "title"))
		if title == "" {
			return invalid()
		}
		city := strings.TrimSpace(nodeAttr(cells[2], "title"))
		if city == "" {
			city = nodeText(cells[2])
		}
		if strings.Contains(city, "...") || strings.Contains(city, "…") {
			return invalid()
		}
		ref := PostingRef{ExternalID: id, URL: kedacomJobURL(id), Title: title, Company: "苏州科达", JobType: "UNKNOWN", Locations: splitPlaces(city)}
		if err := validateRef(ref); err != nil {
			return campusPage{}, err
		}
		out.Refs = append(out.Refs, ref)
	}
	return out, nil
}

func (a PublicPlatform) fetchKedacom(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != kedacomJobURL(r.ExternalID) || r.Company != "苏州科达" {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	root, err := a.document(ctx, s, r.URL, nil)
	if err != nil {
		return "", err
	}
	return kedacomDetail(root, r)
}

func kedacomDetail(root *html.Node, r PostingRef) (string, error) {
	invalid := func() (string, error) { return "", fail("SCHEMA_INVALID", false, 200) }
	title, err := uniqueNode(root, func(n *html.Node) bool {
		return nodeClass(n, "boxSupertitle") && len(nodes(n, func(x *html.Node) bool { return nodeClass(x, "applyStatus") })) == 1
	})
	if err != nil || nodeText(title) != r.Title {
		return invalid()
	}
	apply, err := uniqueNode(root, func(n *html.Node) bool { return n.Data == "a" && nodeAttr(n, "id") == "apply" })
	if err != nil {
		return invalid()
	}
	u, err := url.Parse(nodeAttr(apply, "url"))
	if err != nil || u.IsAbs() || u.Host != "" || u.Path != "/Portal/Resume/ResumeItem" || u.Query().Get("jid") != r.ExternalID {
		return invalid()
	}
	container, err := uniqueNode(root, func(n *html.Node) bool { return nodeClass(n, "xiangqingcontain") })
	if err != nil {
		return invalid()
	}
	fields := map[string]string{}
	for _, label := range nodes(container, func(n *html.Node) bool { return n.Data == "li" && nodeClass(n, "ntitle") }) {
		n := label.NextSibling
		for n != nil && n.Type != html.ElementNode {
			n = n.NextSibling
		}
		if n == nil || n.Data != "li" {
			return invalid()
		}
		key := strings.TrimSuffix(nodeText(label), "：")
		if _, exists := fields[key]; exists {
			return invalid()
		}
		fields[key] = nodeText(n)
	}
	if fields["招聘类别"] != "校园招聘" {
		return invalid()
	}
	body, err := uniqueNode(container, func(n *html.Node) bool { return nodeClass(n, "xiangqingtext") })
	if err != nil {
		return invalid()
	}
	sections := map[string]string{}
	current := ""
	for n := body.FirstChild; n != nil; n = n.NextSibling {
		if nodeClass(n, "title") {
			current = strings.TrimSuffix(nodeText(n), "：")
			if _, exists := sections[current]; exists {
				return invalid()
			}
			sections[current] = ""
		} else if current != "" {
			if text := nodeText(n); text != "" {
				sections[current] += text + "\n"
			}
		}
	}
	text, err := bankPostingText(r, "官网校园招聘类别（不含实习，具体毕业年份以原文为准）", sections["工作职责"], sections["任职资格"])
	if err != nil {
		return "", err
	}
	// Keep published dates and employment facts. An empty deadline stays unknown.
	for _, key := range []string{"工作性质", "工作地点", "发布时间", "截止时间", "薪资范围", "招聘人数"} {
		if value := strings.TrimSpace(fields[key]); value != "" {
			text += fmt.Sprintf("\n官网%s：%s", key, value)
		}
	}
	return text, nil
}
