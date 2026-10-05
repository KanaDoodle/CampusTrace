package source

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const oppoOrigin = "https://careers.oppo.com"
const oppoProjectCode = "30"
const oppoProjectName = "2027届应届生校园招聘"
const oppoPageSize = 50

type oppoEnvelope[T any] struct {
	Code *int `json:"code"`
	Data T    `json:"data"`
}
type oppoProject struct {
	ID          int64  `json:"idRecruitProject"`
	Name        string `json:"projectName"`
	Type        string `json:"recruitmentType"`
	TypeName    string `json:"recruitmentTypeName"`
	Requirement string `json:"recruitRequire"`
}
type oppoPost struct {
	ID          int64  `json:"idRecruitPosition"`
	Project     int64  `json:"projectId"`
	ProjectName string `json:"projectName"`
	Type        string `json:"recruitmentType"`
	TypeName    string `json:"recruitmentTypeName"`
	Name        string `json:"positionName"`
	City        string `json:"workCityName"`
	Cities      []struct {
		Name string `json:"workCityName"`
	} `json:"workCityVOList"`
	Description  string `json:"positionDesc"`
	Requirement  string `json:"positionRequire"`
	Knowledge    string `json:"knowledgeSkill"`
	AICapability string `json:"aiCapabilityLevelDesc"`
	Bonus        string `json:"bonusItem"`
}

func oppoOK[T any](v oppoEnvelope[T]) error {
	if v.Code != nil && (*v.Code == 401 || *v.Code == 403) {
		return fail("BLOCKED", false, 200)
	}
	if v.Code == nil || *v.Code != 0 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}

// The published anonymous client uses a public tenant identifier, without any
// candidate session. Keep caller cookies out of this operation.
func (a PublicPlatform) oppoPublic(s d.Source) (PublicPlatform, error) {
	if _, err := PlatformURL(s); err != nil {
		return a, err
	}
	base := a.Client
	if base == nil {
		base = publicPlatformClient
	}
	client := *base
	client.Jar = nil
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || req.URL.Host != "careers.oppo.com" || len(via) > 4 {
			return fmt.Errorf("public recruiting redirect outside origin")
		}
		if base.CheckRedirect != nil {
			return base.CheckRedirect(req, via)
		}
		return nil
	}
	a.Client = &client
	return a, nil
}
func (a PublicPlatform) oppoScope(ctx context.Context, s d.Source) (oppoProject, error) {
	var v oppoEnvelope[[]oppoProject]
	if err := a.get(ctx, s, oppoOrigin+"/openapi/position/project/list", &v); err != nil {
		return oppoProject{}, err
	}
	if err := oppoOK(v); err != nil {
		return oppoProject{}, err
	}
	var project oppoProject
	count := 0
	for _, row := range v.Data {
		if row.ID == 30 {
			project = row
			count++
		}
	}
	if count != 1 || project.Name != oppoProjectName || project.Type != "Graduate" || project.TypeName != "应届生" || strings.TrimSpace(project.Requirement) == "" {
		return project, fail("SCHEMA_INVALID", false, 200)
	}
	return project, nil
}
func oppoRef(row oppoPost, detail bool) (PostingRef, error) {
	// The detail endpoint omits recruitmentType, but retains the project and
	// published type name. Do not confuse ATS position IDs with public row IDs.
	if row.ID <= 0 || row.Project != 30 || row.ProjectName != oppoProjectName || row.TypeName != "应届生" || (!detail && row.Type != "Graduate") || (detail && row.Type != "" && row.Type != "Graduate") {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	locations := []string{}
	seen := map[string]bool{}
	add := func(name string) {
		for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == ',' || r == '，' || r == '、' || r == ';' || r == '；' }) {
			part = strings.TrimSpace(part)
			if part != "" && !seen[part] {
				locations = append(locations, part)
				seen[part] = true
			}
		}
	}
	add(row.City)
	for _, city := range row.Cities {
		add(city.Name)
	}
	id := strconv.FormatInt(row.ID, 10)
	ref := PostingRef{ExternalID: id, URL: oppoOrigin + "/university/oppo/campus/post/" + id, Title: row.Name, Company: "OPPO", JobType: "FULL_TIME", Locations: locations}
	return ref, validateRef(ref)
}
func (a PublicPlatform) oppoPage(ctx context.Context, s d.Source, page int) ([]PostingRef, int, error) {
	var v oppoEnvelope[struct {
		Total *int       `json:"total"`
		Page  int        `json:"current"`
		Size  int        `json:"size"`
		Pages int        `json:"pages"`
		Rows  []oppoPost `json:"records"`
	}]
	input := map[string]any{"pageNum": page, "pageSize": oppoPageSize, "positionName": "", "projectList": []any{map[string]any{"projectId": 30, "recruitmentType": "Graduate", "isAllNode": "Y", "themeList": []any{}}}, "positionTypeList": []any{}, "workCityCodeList": []any{}, "shareId": ""}
	if err := a.post(ctx, s, oppoOrigin+"/openapi/position/pageNew", input, &v); err != nil {
		return nil, 0, err
	}
	if err := oppoOK(v); err != nil {
		return nil, 0, err
	}
	if v.Data.Total == nil || *v.Data.Total < 0 || v.Data.Rows == nil {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	total := *v.Data.Total
	if total > MaxPostings {
		return nil, 0, fail("CAPACITY", false, 200)
	}
	if v.Data.Page != page || v.Data.Size != oppoPageSize || v.Data.Pages != (total+oppoPageSize-1)/oppoPageSize || len(v.Data.Rows) != min(oppoPageSize, max(0, total-(page-1)*oppoPageSize)) {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	refs := []PostingRef{}
	for _, row := range v.Data.Rows {
		ref, err := oppoRef(row, false)
		if err != nil {
			return nil, 0, err
		}
		refs = append(refs, ref)
	}
	return refs, total, nil
}
func (a PublicPlatform) previewOPPO(ctx context.Context, site CampusSite) (CampusPreview, error) {
	s := d.Source{ID: "oppo-preview", Adapter: "oppo", Tenant: oppoProjectCode, RateLimit: 30}
	a, err := a.oppoPublic(s)
	if err != nil {
		return CampusPreview{}, err
	}
	if _, err := a.oppoScope(ctx, s); err != nil {
		return CampusPreview{}, err
	}
	refs, total, err := a.oppoPage(ctx, s, 1)
	return CampusPreview{URL: site.URL, Name: site.Company + " · " + site.Scope, ProjectCode: s.Tenant, Total: total, Samples: refs[:min(5, len(refs))]}, err
}
func (a PublicPlatform) discoverOPPO(ctx context.Context, s d.Source, w d.WatchTarget) ([]PostingRef, error) {
	if w.Direction != "" {
		return nil, fail("UNSUPPORTED", false, 0)
	}
	a, err := a.oppoPublic(s)
	if err != nil {
		return nil, err
	}
	if _, err := a.oppoScope(ctx, s); err != nil {
		return nil, err
	}
	refs, total, err := a.oppoPage(ctx, s, 1)
	if err != nil {
		return nil, err
	}
	for page := 2; page <= (total+oppoPageSize-1)/oppoPageSize; page++ {
		rows, count, err := a.oppoPage(ctx, s, page)
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
func (a PublicPlatform) fetchOPPO(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	id, err := strconv.ParseInt(r.ExternalID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != r.ExternalID || r.URL != oppoOrigin+"/university/oppo/campus/post/"+r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	a, err = a.oppoPublic(s)
	if err != nil {
		return "", err
	}
	project, err := a.oppoScope(ctx, s)
	if err != nil {
		return "", err
	}
	var v oppoEnvelope[oppoPost]
	if err := a.get(ctx, s, oppoOrigin+"/openapi/position/detail?id="+r.ExternalID, &v); err != nil {
		return "", err
	}
	if err := oppoOK(v); err != nil {
		return "", err
	}
	ref, err := oppoRef(v.Data, true)
	duties, requirements := plainHTML(v.Data.Description), plainHTML(v.Data.Requirement)
	if err != nil || ref.ExternalID != r.ExternalID || strings.TrimSpace(duties) == "" || strings.TrimSpace(requirements) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text := fmt.Sprintf("岗位名称：%s\n招聘范围：OPPO · %s\n工作地点：%s\n官网公开毕业要求：\n%s\n岗位职责：\n%s\n任职要求：\n%s", ref.Title, project.Name, strings.Join(ref.Locations, "、"), plainHTML(project.Requirement), duties, requirements)
	for _, section := range []struct{ name, text string }{{"知识与技能要求", v.Data.Knowledge}, {"AI 能力要求", v.Data.AICapability}, {"加分项", v.Data.Bonus}} {
		if part := plainHTML(section.text); strings.TrimSpace(part) != "" {
			text += "\n" + section.name + "：\n" + part
		}
	}
	return text, nil
}
