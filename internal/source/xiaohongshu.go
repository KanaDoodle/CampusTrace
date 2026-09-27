package source

import (
	"context"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const XHSURL = "https://job.xiaohongshu.com/campus/position"
const xhsBase = "https://job.xiaohongshu.com/websiterecruit/position"

var xhsID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

type CampusPreview struct {
	URL         string       `json:"url"`
	Name        string       `json:"name"`
	ProjectCode string       `json:"project_code"`
	Total       int          `json:"total"`
	Samples     []PostingRef `json:"samples"`
}

func RecognizeCampusURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "job.xiaohongshu.com" || strings.TrimRight(u.Path, "/") != "/campus/position" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fail("UNSUPPORTED", false, 0)
	}
	return nil
}

type xhsEnvelope[T any] struct {
	StatusCode int  `json:"statusCode"`
	Success    bool `json:"success"`
	Data       T    `json:"data"`
}
type xhsProject struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	ExtMap struct {
		Category string `json:"category"`
	} `json:"extMap"`
}
type xhsPosition struct {
	ID            int64  `json:"positionId"`
	Name          string `json:"positionName"`
	Workplace     string `json:"workplace"`
	ProjectName   string `json:"jobProjectName"`
	Direction     string `json:"direction"`
	Duty          string `json:"duty"`
	Qualification string `json:"qualification"`
}
type xhsPage struct {
	PageNum   int           `json:"pageNum"`
	Total     int           `json:"total"`
	TotalPage int           `json:"totalPage"`
	List      []xhsPosition `json:"list"`
}
type xhsDetail struct {
	xhsPosition
	Project           string `json:"jobProject"`
	Status            string `json:"recruitStatus"`
	ApplyLimit        int    `json:"applyLimit"`
	DirectionLimit    int    `json:"directionLimit"`
	SubDirectionLimit int    `json:"subDirectionLimit"`
}

func (a PublicPlatform) xhsPage(ctx context.Context, s d.Source, page int) (xhsPage, error) {
	var result xhsEnvelope[xhsPage]
	body := map[string]any{"positionName": "", "pageNum": page, "pageSize": 100, "recruitType": "campus", "jobProjects": []string{s.Tenant}}
	if err := a.post(ctx, s, xhsBase+"/pageQueryPosition", body, &result); err != nil {
		return xhsPage{}, err
	}
	if !result.Success || result.StatusCode != 200 || result.Data.Total < 0 || result.Data.Total > MaxPostings || result.Data.PageNum != page || result.Data.List == nil || result.Data.TotalPage != (result.Data.Total+99)/100 {
		return xhsPage{}, fail("SCHEMA_INVALID", false, 200)
	}
	return result.Data, nil
}

func (a PublicPlatform) PreviewXHS(ctx context.Context, raw string) (CampusPreview, error) {
	if err := RecognizeCampusURL(raw); err != nil {
		return CampusPreview{}, err
	}
	s := d.Source{ID: "xiaohongshu-preview", RateLimit: 30}
	var enums xhsEnvelope[map[string][]xhsProject]
	if err := a.post(ctx, s, "https://job.xiaohongshu.com/websiterecruit/common/findEnumList", []string{"PositionProjectEnum"}, &enums); err != nil {
		return CampusPreview{}, err
	}
	if !enums.Success || enums.StatusCode != 200 {
		return CampusPreview{}, fail("SCHEMA_INVALID", false, 200)
	}
	var chosen *xhsProject
	for _, project := range enums.Data["PositionProjectEnum"] {
		if project.ExtMap.Category == "regular" {
			if chosen != nil || !tenantPattern.MatchString(project.Code) || project.Name == "" {
				return CampusPreview{}, fail("SCHEMA_INVALID", false, 200)
			}
			copy := project
			chosen = &copy
		}
	}
	if chosen == nil {
		return CampusPreview{}, fail("SCHEMA_INVALID", false, 200)
	}
	s.Tenant = chosen.Code
	page, err := a.xhsPage(ctx, s, 1)
	if err != nil {
		return CampusPreview{}, err
	}
	preview := CampusPreview{URL: XHSURL, Name: chosen.Name, ProjectCode: chosen.Code, Total: page.Total, Samples: []PostingRef{}}
	for _, row := range page.List {
		ref, err := xhsRef(row)
		if err != nil {
			return CampusPreview{}, err
		}
		if len(preview.Samples) < 5 {
			preview.Samples = append(preview.Samples, ref)
		}
	}
	return preview, nil
}

func xhsRef(row xhsPosition) (PostingRef, error) {
	if row.ID <= 0 || strings.TrimSpace(row.Name) == "" || len(row.Name) > 300 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	id := strconv.FormatInt(row.ID, 10)
	ref := PostingRef{ExternalID: id, URL: XHSURL + "/" + id, Title: row.Name, Company: "小红书", JobType: "FULL_TIME", Locations: splitXHSLocations(row.Workplace)}
	return ref, validateRef(ref)
}
func splitXHSLocations(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == '，' || r == ',' || r == '、' })
	out := []string{}
	for _, part := range parts {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (a PublicPlatform) discoverXHS(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if !tenantPattern.MatchString(s.Tenant) {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	first, err := a.xhsPage(ctx, s, 1)
	if err != nil {
		return nil, err
	}
	all := []PostingRef{}
	seenRows := 0
	for page := 1; page <= first.TotalPage; page++ {
		data := first
		if page > 1 {
			data, err = a.xhsPage(ctx, s, page)
			if err != nil {
				return nil, err
			}
			if data.Total != first.Total {
				return nil, fail("SCHEMA_INVALID", false, 200)
			}
		}
		for _, row := range data.List {
			seenRows++
			if row.ProjectName == "" {
				return nil, fail("SCHEMA_INVALID", false, 200)
			}
			ref, err := xhsRef(row)
			if err != nil {
				return nil, err
			}
			if w.Direction == "" || row.Direction == w.Direction {
				all = append(all, ref)
			}
		}
	}
	// An incomplete source must never silently look like a complete scan.
	if seenRows != first.Total {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return all, nil
}

func (a PublicPlatform) fetchXHS(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !xhsID.MatchString(r.ExternalID) || r.URL != XHSURL+"/"+r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var result xhsEnvelope[xhsDetail]
	raw := xhsBase + "/queryPositionDetail?positionId=" + url.QueryEscape(r.ExternalID)
	if err := a.get(ctx, s, raw, &result); err != nil {
		return "", err
	}
	v := result.Data
	if !result.Success || result.StatusCode != 200 || strconv.FormatInt(v.ID, 10) != r.ExternalID || v.Project != s.Tenant || strings.TrimSpace(v.Duty+v.Qualification) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text := fmt.Sprintf("岗位名称：%s\n招聘项目：%s\n工作地点：%s\n工作职责：\n%s\n任职资格：\n%s\n官网招聘状态：%s", v.Name, v.ProjectName, v.Workplace, v.Duty, v.Qualification, v.Status)
	if v.ApplyLimit > 0 {
		text += fmt.Sprintf("\n网站列出的最多可投职位数：%d", v.ApplyLimit)
	}
	if v.DirectionLimit > 0 {
		text += fmt.Sprintf("\n网站列出的最多可投职位方向数：%d", v.DirectionLimit)
	}
	if v.SubDirectionLimit > 0 {
		text += fmt.Sprintf("\n网站列出的最多可投子方向数：%d", v.SubDirectionLimit)
	}
	return text, nil
}
