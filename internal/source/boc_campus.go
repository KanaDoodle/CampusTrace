package source

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const BOCCampusURL = "https://campus.chinahr.com/pages/2027-boc/"
const bocOrigin = "https://ats.chinahr.com"
const bocRoot = "6a82a408f6bf3d0ac213d550"
const bocProject = "6a82a408f6bf3d0ac213d518"

// This is the public project selector published in the official page, not a candidate token.
const bocPublicProject = "3efebbc0-ba85-4199-9999-6c3c1c0e8cf7"
const bocConditionsURL = "https://www.boc.cn/aboutboc/bi4/202609/t20260903_25689311.html"

var bankHex24 = regexp.MustCompile(`^[a-f0-9]{24}$`)
var bocUnits = map[string]struct {
	Name, ID string
	Cities   map[string]string
}{
	"boc_software": {"软件中心", "6a82a408f6bf3d0ac213d568", map[string]string{
		"6a82a408f6bf3d0ac213d569": "软件中心（北京）", "6a82a408f6bf3d0ac213d56a": "软件中心（深圳）", "6a82a408f6bf3d0ac213d56b": "软件中心（西安）", "6a82a408f6bf3d0ac213d56c": "软件中心（合肥）", "6a82a408f6bf3d0ac213d56d": "软件中心（上海）", "6a82a408f6bf3d0ac213d56e": "软件中心（武汉）", "6a82a408f6bf3d0ac213d56f": "软件中心（成都）",
	}},
	"boc_operations": {"信息科技运营中心", "6a82a408f6bf3d0ac213d563", map[string]string{
		"6a82a408f6bf3d0ac213d564": "信息科技运营中心（北京）", "6a82a408f6bf3d0ac213d565": "信息科技运营中心（上海）", "6a82a408f6bf3d0ac213d566": "信息科技运营中心（合肥）", "6a82a408f6bf3d0ac213d567": "信息科技运营中心（内蒙古）",
	}},
}

// The fragment selector distinguishes our two presets. The official page opens
// its group directory; it does not promise to apply a CampusTrace-only filter.
func bocCampusURL(adapter string) string { return BOCCampusURL + "#/jobs?campustrace_scope=" + adapter }
func bocJobURL(id string) string         { return "https://applyjob.chinahr.com/apply/job/wish/" + id }

type bocEnvelope[T any] struct {
	Code *int `json:"code"`
	Data T    `json:"retMsg"`
}
type bocJob struct {
	ID          string `json:"id"`
	Title       string `json:"name"`
	UnitID      string `json:"companyId"`
	Unit        string `json:"companyName"`
	Address     string `json:"address"`
	Description string `json:"jobDesc"`
	Education   string `json:"education"`
	Experience  string `json:"experience"`
	Shown       *int   `json:"isShow"`
	Deadline    *int64 `json:"applyEndTime"`
}

func bocRef(adapter string, v bocJob) (PostingRef, error) {
	unit, ok := bocUnits[adapter]
	if !ok || !bankHex24.MatchString(v.ID) || unit.Cities[v.UnitID] != v.Unit || v.Unit == "" || v.Experience != "应届毕业生" || v.Shown == nil || *v.Shown != 1 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: v.ID, URL: bocJobURL(v.ID), Title: v.Title, Company: "中国银行 · " + v.Unit, JobType: "UNKNOWN", Locations: splitPlaces(strings.TrimSpace(v.Address))}
	return r, validateRef(r)
}
func (a PublicPlatform) discoverBOC(ctx context.Context, s d.Source) ([]PostingRef, error) {
	unit := bocUnits[s.Adapter]
	var catalog bocEnvelope[[]struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Parent  string `json:"parentId"`
		Project string `json:"projectId"`
	}]
	q := url.Values{"token": {bocPublicProject}, "page": {"1"}, "pageSize": {"10000"}}
	if err := a.get(ctx, s, bocOrigin+"/api/company/list?"+q.Encode(), &catalog); err != nil {
		return nil, err
	}
	if catalog.Code == nil || *catalog.Code != 1 || catalog.Data == nil || len(catalog.Data) >= 10000 {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	rootFound, unitFound := false, false
	seen := map[string]bool{}
	cities := []string{}
	for _, row := range catalog.Data {
		if !bankHex24.MatchString(row.ID) || seen[row.ID] || row.Project != bocProject {
			return nil, fail("SCHEMA_INVALID", false, 200)
		}
		seen[row.ID] = true
		if row.ID == bocRoot {
			rootFound = row.Name == "中国银行2027年全球校园招聘" && row.Parent == ""
		}
		if row.ID == unit.ID {
			unitFound = row.Name == unit.Name
		}
		if row.Parent == unit.ID {
			if unit.Cities[row.ID] != row.Name {
				return nil, fail("SCHEMA_INVALID", false, 200)
			}
			cities = append(cities, row.ID)
		}
	}
	if !rootFound || !unitFound || len(cities) != len(unit.Cities) {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	refs := []PostingRef{}
	jobIDs := map[string]bool{}
	for _, city := range cities {
		rows, err := campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
			var v struct {
				bocEnvelope[[]bocJob]
				Total *int `json:"totalCount"`
			}
			q := url.Values{"token": {bocPublicProject}, "page": {strconv.Itoa(page)}, "pageSize": {"50"}, "companyId": {city}}
			if err := a.get(ctx, s, bocOrigin+"/api/job/list?"+q.Encode(), &v); err != nil {
				return campusPage{}, err
			}
			if v.Code == nil || *v.Code != 1 || v.Total == nil || v.Data == nil {
				return campusPage{}, fail("SCHEMA_INVALID", false, 200)
			}
			out := campusPage{Refs: []PostingRef{}, Total: *v.Total, Page: page, Size: 50}
			for _, row := range v.Data {
				r, err := bocRef(s.Adapter, row)
				if err != nil || row.UnitID != city {
					return out, fail("SCHEMA_INVALID", false, 200)
				}
				out.Refs = append(out.Refs, r)
			}
			return out, nil
		})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if jobIDs[row.ExternalID] {
				return nil, fail("SCHEMA_DUPLICATE_ID", false, 200)
			}
			jobIDs[row.ExternalID] = true
			refs = append(refs, row)
		}
		if len(refs) > MaxPostings {
			return nil, fail("CAPACITY", false, 200)
		}
	}
	return refs, nil
}
func (a PublicPlatform) fetchBOC(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !bankHex24.MatchString(r.ExternalID) || r.URL != bocJobURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v bocEnvelope[bocJob]
	if err := a.get(ctx, s, bocOrigin+"/api/job/detail/"+r.ExternalID, &v); err != nil {
		return "", err
	}
	actual, err := bocRef(s.Adapter, v.Data)
	if err != nil || v.Code == nil || *v.Code != 1 || actual.ExternalID != r.ExternalID || actual.Title != r.Title || actual.Company != r.Company {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	raw := plainHTML(strings.ReplaceAll(v.Data.Description, `\n`, "\n"))
	parts := strings.Split(raw, "招聘条件及要求：")
	if len(parts) != 2 {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	duties := strings.TrimSpace(strings.TrimPrefix(parts[0], "职位介绍："))
	text, err := bankPostingText(actual, "中国银行2027年全球校园招聘 · "+bocUnits[s.Adapter].Name, duties, parts[1])
	if err == nil {
		text += "\n官网学历字段：" + v.Data.Education + "\n官网应聘经验分类：" + v.Data.Experience
		// Some qualifications are referenced in an attached PDF. Keep the link
		// and the limitation instead of inventing English/major requirements.
		text += "\n其他资格条件引用：" + bocConditionsURL + "（公告附件中的招聘条件未在本岗位正文展开，请自行核对）"
		if v.Data.Deadline != nil && *v.Data.Deadline > 0 {
			text += "\n官网截止日期（北京时间）：" + time.UnixMilli(*v.Data.Deadline).In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02 15:04")
		}
	}
	return text, err
}
