package source

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"golang.org/x/net/html"
)

const sczqOrigin = "https://sczq.zhiye.com"
const SCZQCampusURL = sczqOrigin + "/campus/?r=2"

func sczqJobURL(id string) string { return sczqOrigin + "/zpdetail/" + id }

// This portal uses the legacy listtable template, not the modern Beisen API
// or Kedacom's jobsTable template. r=2 binds the full campus category; c is a
// city filter and must not be used as a recruitment project.
func (a PublicPlatform) discoverSCZQ(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		root, err := a.document(ctx, s, SCZQCampusURL+"&PageIndex="+strconv.Itoa(page), nil)
		if err != nil {
			return campusPage{}, err
		}
		return sczqPage(root, page)
	})
}

func sczqPage(root *html.Node, page int) (campusPage, error) {
	invalid := func() (campusPage, error) { return campusPage{}, fail("SCHEMA_INVALID", false, 200) }
	table, err := uniqueNode(root, func(n *html.Node) bool { return n.Data == "table" && nodeClass(n, "listtable") })
	if err != nil {
		return campusPage{}, err
	}
	count, err := uniqueNode(root, func(n *html.Node) bool { return nodeClass(n, "tablenote") })
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
	head, err := uniqueNode(table, func(n *html.Node) bool { return n.Data == "tr" && nodeClass(n, "tabletitle") })
	if err != nil {
		return campusPage{}, err
	}
	columns := nodes(head, func(n *html.Node) bool { return n.Data == "th" })
	if len(columns) != 4 || nodeText(columns[0]) != "职位名称" || nodeText(columns[2]) != "工作地点" {
		return invalid()
	}
	out := campusPage{Refs: []PostingRef{}, Total: total, Page: current, Size: 15}
	for _, row := range nodes(table, func(n *html.Node) bool { return n.Data == "tr" }) {
		if row == head {
			continue
		}
		cells := nodes(row, func(n *html.Node) bool { return n.Data == "td" })
		if len(cells) != 4 {
			return invalid()
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
		if err != nil || !numericID(id) || nodeAttr(link, "jobadid") != id || len(q) != 2 || len(q["r"]) != 1 || q.Get("r") != "2" || len(q["PageIndex"]) != 1 || q.Get("PageIndex") != strconv.Itoa(page) {
			return invalid()
		}
		title := strings.TrimSpace(nodeAttr(link, "title"))
		city := strings.TrimSpace(nodeAttr(cells[2], "title"))
		if city == "" {
			city = nodeText(cells[2])
		}
		if title == "" || strings.Contains(city, "...") || strings.Contains(city, "…") {
			return invalid()
		}
		ref := PostingRef{ExternalID: id, URL: sczqJobURL(id), Title: title, Company: "首创证券", JobType: "UNKNOWN", Locations: splitPlaces(city)}
		if err := validateRef(ref); err != nil {
			return campusPage{}, err
		}
		out.Refs = append(out.Refs, ref)
	}
	return out, nil
}

func (a PublicPlatform) fetchSCZQ(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != sczqJobURL(r.ExternalID) || r.Company != "首创证券" {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	root, err := a.document(ctx, s, r.URL, nil)
	if err != nil {
		return "", err
	}
	return sczqDetail(root, r)
}

func sczqDetail(root *html.Node, r PostingRef) (string, error) {
	invalid := func() (string, error) { return "", fail("SCHEMA_INVALID", false, 200) }
	title, err := uniqueNode(root, func(n *html.Node) bool { return nodeClass(n, "xiangqingtitle") })
	if err != nil || nodeText(title) != r.Title {
		return invalid()
	}
	apply, err := uniqueNode(root, func(n *html.Node) bool { return n.Data == "a" && nodeAttr(n, "id") == "apply" })
	if err != nil {
		return invalid()
	}
	u, err := url.Parse(nodeAttr(apply, "url"))
	if err != nil || u.IsAbs() || u.Host != "" || u.RawPath != "" || u.Fragment != "" || u.Path != "/Portal/Resume/ResumeItem" {
		return invalid()
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["jid"]) != 1 || q.Get("jid") != r.ExternalID {
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
		if n == nil || n.Data != "li" || !nodeClass(n, "nvalue") {
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
		value := nodeText(n)
		if n.Type == html.ElementNode && n.Data == "p" && (value == "工作地点：" || value == "工作职责：" || value == "任职资格：") {
			current = strings.TrimSuffix(value, "：")
			if _, exists := sections[current]; exists {
				return invalid()
			}
			sections[current] = ""
		} else if current != "" && value != "" {
			sections[current] += value + "\n"
		}
	}
	text, err := bankPostingText(r, "官网校园招聘类别（含不同招聘单位；届别、用工形式与签约单位以岗位原文为准）", sections["工作职责"], sections["任职资格"])
	if err != nil {
		return "", err
	}
	if value := strings.TrimSpace(sections["工作地点"]); value != "" {
		text += "\n官网工作地点：" + value
	}
	for _, key := range []string{"工作性质", "发布时间", "截止时间", "薪资范围", "招聘人数"} {
		if value := strings.TrimSpace(fields[key]); value != "" {
			text += fmt.Sprintf("\n官网%s：%s", key, value)
		}
	}
	return text, nil
}
