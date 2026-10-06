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

const tclOrigin = "https://campus.tcl.com"
const tclProject = "308501"

var tclUnits = map[string]struct{ Page, ID, Name string }{
	"tcl_digital": {"43", "101206", "流程与数字化转型中心"},
	"tcl_honghu":  {"46", "364906", "鸿鹄实验室"},
}
var tclID = regexp.MustCompile(`^[a-f0-9]{24}$`)

func tclCampusURL(adapter string) string {
	return tclOrigin + "/campus/recruiting.html?id=" + tclUnits[adapter].Page
}
func tclJobURL(id string) string {
	return "https://wecruit.hotjob.cn/SU64893571bef57c16d356b99e/pb/posDetail.html?postId=" + id + "&postType=campus"
}
func (a PublicPlatform) verifyTCL(ctx context.Context, s d.Source) error {
	root, err := a.document(ctx, s, tclCampusURL(s.Adapter), nil)
	if err != nil {
		return err
	}
	unit := tclUnits[s.Adapter]
	project, err := uniqueNode(root, func(n *html.Node) bool {
		return n.Data == "input" && nodeAttr(n, "name") == "job_xm[]" && nodeAttr(n, "value") == tclProject
	})
	if err != nil || !strings.Contains(nodeText(project.Parent), "TCL 2027届全球校园招聘") || !strings.Contains(nodeText(project.Parent), "2026年9月-2027年12月") {
		return fail("SCHEMA_INVALID", false, 200)
	}
	n, err := uniqueNode(root, func(n *html.Node) bool {
		return n.Data == "input" && nodeAttr(n, "name") == "cy_type[]" && nodeAttr(n, "id") == "cy_type_"+unit.Page
	})
	if err != nil || nodeAttr(n, "value") != unit.ID || !strings.Contains(nodeText(n.Parent), unit.Name) {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) tclRows(ctx context.Context, s d.Source, title string) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v struct {
			Status  string `json:"title"`
			Total   *int   `json:"total_counts"`
			Content string `json:"content"`
		}
		body := url.Values{"keyType": {"1"}, "cate_id": {"100"}, "keys": {title}, "job_xm[]": {tclProject}, "cy_type[]": {tclUnits[s.Adapter].ID}}
		if err := a.post(ctx, s, tclOrigin+"/Ajax/campus_search.html?page="+strconv.Itoa(page), body, &v); err != nil {
			return campusPage{}, err
		}
		if v.Status != "success" || v.Total == nil || v.Content == "" {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		root, err := html.Parse(strings.NewReader(v.Content))
		if err != nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Total, Page: page, Size: 10}
		if out.Total > 0 {
			active, err := uniqueNode(root, func(n *html.Node) bool {
				return n.Data == "a" && nodeClass(n, "active") && !nodeClass(n, "next")
			})
			if err != nil || nodeText(active) != strconv.Itoa(page) || nodeAttr(active, "onclick") != "javascript:job_search("+strconv.Itoa(page)+")" {
				return out, fail("SCHEMA_INVALID", false, 200)
			}
		}
		for _, item := range nodes(root, func(n *html.Node) bool { return nodeClass(n, "proInfoConList") }) {
			head, err := uniqueNode(item, func(n *html.Node) bool { return nodeClass(n, "head") })
			if err != nil || nodeAttr(head, "data-recruittype") != "1" || !tclID.MatchString(nodeAttr(head, "data-postid")) {
				return out, fail("SCHEMA_INVALID", false, 200)
			}
			id := nodeAttr(head, "data-postid")
			name, err := uniqueNode(head, func(n *html.Node) bool { return nodeClass(n, "name") })
			if err != nil {
				return out, err
			}
			tag, err := uniqueNode(head, func(n *html.Node) bool { return nodeClass(n, "tag") })
			if err != nil {
				return out, err
			}
			fields := nodes(tag, func(n *html.Node) bool { return n.Data == "span" })
			link, err := uniqueNode(item, func(n *html.Node) bool { return nodeClass(n, "tool-btn") })
			if len(fields) < 2 || nodeText(fields[0]) != tclUnits[s.Adapter].Name || err != nil || nodeAttr(link, "href") != tclJobURL(id) {
				return out, fail("SCHEMA_INVALID", false, 200)
			}
			// RecruitType=1 means campus, not a documented employment type.
			out.Refs = append(out.Refs, PostingRef{ExternalID: id, URL: tclJobURL(id), Title: nodeText(name), Company: "TCL · " + tclUnits[s.Adapter].Name, JobType: "UNKNOWN", Locations: splitPlaces(nodeText(fields[1]))})
		}
		return out, nil
	})
}
func (a PublicPlatform) discoverTCL(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.verifyTCL(ctx, s); err != nil {
		return nil, err
	}
	return a.tclRows(ctx, s, "")
}
func (a PublicPlatform) fetchTCL(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !tclID.MatchString(r.ExternalID) || r.URL != tclJobURL(r.ExternalID) || validateRef(r) != nil {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	if err := a.verifyTCL(ctx, s); err != nil {
		return "", err
	}
	// Detail JSON does not echo an ID or title. Bind it to a fresh title search
	// in the same project/unit, including every page and duplicate title.
	refs, err := a.tclRows(ctx, s, r.Title)
	if err != nil {
		return "", err
	}
	var actual PostingRef
	for _, ref := range refs {
		if ref.ExternalID == r.ExternalID && ref.Title == r.Title {
			actual = ref
		}
	}
	if actual.ExternalID == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	var v struct {
		Status  string `json:"title"`
		Duty    string `json:"workContent"`
		Require string `json:"serviceCondition"`
	}
	if err := a.post(ctx, s, tclOrigin+"/Ajax/job_detail.html?postid="+r.ExternalID+"&recruitType=1", url.Values{}, &v); err != nil {
		return "", err
	}
	if v.Status != "success" || strings.TrimSpace(plainHTML(v.Duty)) == "" || strings.TrimSpace(plainHTML(v.Require)) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return "岗位名称：" + actual.Title + "\n官网招聘部门：" + actual.Company + "\n招聘项目：TCL 2027届全球校园招聘\n毕业时间范围：2026年9月-2027年12月\n工作地点：" + strings.Join(actual.Locations, "、") + "\n岗位职责：\n" + plainHTML(v.Duty) + "\n任职要求：\n" + plainHTML(v.Require), nil
}
