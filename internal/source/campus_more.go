package source

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

var hex32 = regexp.MustCompile(`^[a-f0-9]{32}$`)
var errCampusOffOriginRedirect = errors.New("public recruiting redirect outside origin")

const LenovoCampusURL = "https://talent.lenovo.com.cn/#/campus"
const MideaCampusURL = "https://careers.midea.com/schoolOut/post?projectType=1"
const BYDCampusURL = "https://job.byd.com/portal/mobile/schoolPositionList?tab=%E5%BA%94%E5%B1%8A%E7%94%9F"
const HikvisionCampusURL = "https://campushr.hikvision.com/school?schoolType=nozxf"
const mideaProject = "055bb05d-1957-4ea0-bb21-873ca0164d84"
const bydProject = "2076475538687475714"
const hikvisionProject = "e198653730e14820b9e95b29fbc2223f"

// Public ATS operations never inherit a candidate's cookie jar or follow an
// off-origin redirect. The normal PublicClient still validates DNS and IPs.
func (a PublicPlatform) campusPublic(s d.Source) (PublicPlatform, error) {
	base, err := PlatformURL(s)
	if err != nil {
		return a, err
	}
	origin, _ := url.Parse(base)
	original := a.Client
	if original == nil {
		original = publicPlatformClient
	}
	client := *original
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != origin.Host || len(via) > 4 {
			return errCampusOffOriginRedirect
		}
		if original.CheckRedirect != nil {
			return original.CheckRedirect(req, via)
		}
		return nil
	}
	a.Client = &client
	return a, nil
}

type campusPage struct {
	Refs              []PostingRef
	Total, Page, Size int
}

// Exact page lengths, stable totals, unique IDs and a bounded scan are required
// before keyword filtering. No truncated success is returned to the worker.
func campusPages(ctx context.Context, load func(context.Context, int) (campusPage, error)) ([]PostingRef, error) {
	refs := []PostingRef{}
	seen := map[string]bool{}
	total, size := -1, 0
	for page := 1; ; page++ {
		v, err := load(ctx, page)
		if err != nil {
			return nil, err
		}
		if v.Total > MaxPostings {
			return nil, fail("CAPACITY", false, 200)
		}
		if v.Total < 0 || v.Size <= 0 || v.Size > 100 || v.Page != page || v.Refs == nil || (total >= 0 && (v.Total != total || v.Size != size)) || len(v.Refs) != min(v.Size, max(0, v.Total-(page-1)*v.Size)) {
			return nil, fail("SCHEMA_INVALID", false, 200)
		}
		total, size = v.Total, v.Size
		for _, ref := range v.Refs {
			if err := validateRef(ref); err != nil {
				return nil, err
			}
			if seen[ref.ExternalID] {
				return nil, fail("SCHEMA_DUPLICATE_ID", false, 200)
			}
			seen[ref.ExternalID] = true
			refs = append(refs, ref)
		}
		if len(refs) == total {
			return refs, nil
		}
	}
}
func moreTenant(adapter string) string {
	if adapter == "ctrip" {
		return "campus"
	}
	if _, ok := beisenCompanies[adapter]; ok {
		return "campus"
	}
	return map[string]string{"lenovo": "1", "midea": mideaProject, "byd": bydProject, "hikvision": hikvisionProject, "honor": honorProject}[adapter]
}
func (a PublicPlatform) previewMore(ctx context.Context, site CampusSite) (CampusPreview, error) {
	s := d.Source{ID: site.Adapter + "-preview", Adapter: site.Adapter, Tenant: moreTenant(site.Adapter), RateLimit: 30}
	refs, err := a.discoverMore(ctx, s, d.WatchTarget{})
	if err != nil {
		return CampusPreview{}, err
	}
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: s.Tenant, Total: len(refs), Samples: refs[:min(5, len(refs))]}, nil
}
func (a PublicPlatform) discoverMore(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	var err error
	a, err = a.campusPublic(s)
	if err != nil {
		return nil, err
	}
	if _, ok := beisenCompanies[s.Adapter]; ok {
		return a.discoverBeisen(ctx, s)
	}
	switch s.Adapter {
	case "ctrip":
		return a.discoverCtrip(ctx, s)
	case "lenovo":
		return a.discoverLenovo(ctx, s)
	case "midea":
		return a.discoverMidea(ctx, s)
	case "byd":
		return a.discoverBYD(ctx, s)
	case "honor":
		return a.discoverHonor(ctx, s)
	case "hikvision":
		return a.discoverHikvision(ctx, s)
	}
	return nil, fail("UNSUPPORTED", false, 0)
}
func (a PublicPlatform) fetchMore(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	var err error
	a, err = a.campusPublic(s)
	if err != nil {
		return "", err
	}
	if _, ok := beisenCompanies[s.Adapter]; ok {
		return a.fetchBeisen(ctx, s, r)
	}
	switch s.Adapter {
	case "ctrip":
		return a.fetchCtrip(ctx, s, r)
	case "lenovo":
		return a.fetchLenovo(ctx, s, r)
	case "midea":
		return a.fetchMidea(ctx, s, r)
	case "byd":
		return a.fetchBYD(ctx, s, r)
	case "honor":
		return a.fetchHonor(ctx, s, r)
	case "hikvision":
		return a.fetchHikvision(ctx, s, r)
	}
	return "", fail("UNSUPPORTED", false, 0)
}
func campusText(ref PostingRef, scope, duties, requirements string) (string, error) {
	duties, requirements = plainHTML(duties), plainHTML(requirements)
	if strings.TrimSpace(duties) == "" || strings.TrimSpace(requirements) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return fmt.Sprintf("岗位名称：%s\n公司：%s\n招聘类型：应届生全职\n招聘范围：%s\n工作地点：%s\n岗位职责：\n%s\n任职要求：\n%s", ref.Title, ref.Company, scope, strings.Join(ref.Locations, "、"), duties, requirements), nil
}
func numericID(id string) bool {
	n, err := strconv.ParseInt(id, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == id
}
func splitPlaces(raw string) []string {
	places := []string{}
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == ',' || r == '，' || r == '、' || r == ';' || r == '；' }) {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			places = append(places, part)
			seen[part] = true
		}
	}
	return places
}

func expandedURL(adapter string) string {
	if adapter == "ctrip" {
		return CtripCampusURL
	}
	for _, site := range CampusSites() {
		if site.Adapter == adapter {
			return site.URL
		}
	}
	return ""
}
