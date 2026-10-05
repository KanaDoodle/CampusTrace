package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

const hikvisionOrigin = "https://campushr.hikvision.com"

type hikEnvelope[T any] struct {
	Status  *int  `json:"status"`
	Success *bool `json:"success"`
	Data    T     `json:"data"`
}
type hikBatch struct {
	ID     string `json:"id"`
	Name   string `json:"batchName"`
	Status int    `json:"status"`
	Active int    `json:"yn"`
	Remark string `json:"remark"`
}
type hikPost struct {
	ID           string `json:"id"`
	Batch        string `json:"batchId"`
	BatchName    string `json:"batchName"`
	Name         string `json:"postAdName"`
	FullName     string `json:"batchPositionName"`
	Nature       string `json:"jobNature"`
	Active       int    `json:"yn"`
	Status       int    `json:"status"`
	Merge        *int   `json:"mergeType"`
	Places       string `json:"workPlace"`
	Duties       string `json:"postContent"`
	Requirements string `json:"postRequire"`
}

func hikOK[T any](v hikEnvelope[T]) error {
	if v.Status == nil || *v.Status != 200 || v.Success == nil || !*v.Success {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) hikScope(ctx context.Context, s d.Source) (hikBatch, error) {
	var v hikEnvelope[[]hikBatch]
	if err := a.get(ctx, s, hikvisionOrigin+"/api/userfacade/crsBatch/findNotZxfBatchAll", &v); err != nil {
		return hikBatch{}, err
	}
	if err := hikOK(v); err != nil {
		return hikBatch{}, err
	}
	count := 0
	var result hikBatch
	for _, p := range v.Data {
		if p.ID == hikvisionProject {
			count++
			result = p
		}
	}
	if count != 1 || result.Name != "【2027校园招聘】" || result.Status != 1 || result.Active != 1 || result.Remark == "" {
		return result, fail("SCHEMA_INVALID", false, 200)
	}
	return result, nil
}
func hikURL(row hikPost) string {
	return hikvisionOrigin + "/JobDetails.html?id=" + row.ID + "&type=" + strconv.Itoa(*row.Merge)
}
func hikRef(row hikPost) (PostingRef, error) {
	if row.FullName == "" {
		row.FullName = row.BatchName + row.Name
	}
	if strings.TrimSpace(row.Name) == "" || !hex32.MatchString(row.ID) || row.Batch != hikvisionProject || row.BatchName != "【2027校园招聘】" || row.Nature != "校招应届生" || row.Active != 1 || row.Status != 1 || row.Merge == nil || (*row.Merge != 0 && *row.Merge != 2) || row.FullName != row.BatchName+row.Name {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	ref := PostingRef{ExternalID: row.ID, URL: hikURL(row), Title: row.FullName, Company: "海康威视", JobType: "FULL_TIME", Locations: splitPlaces(row.Places)}
	return ref, validateRef(ref)
}
func (a PublicPlatform) discoverHikvision(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if _, err := a.hikScope(ctx, s); err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v hikEnvelope[struct {
			Rows  []hikPost `json:"list"`
			Total *int      `json:"total"`
			Page  int       `json:"pageNum"`
			Size  int       `json:"pageSize"`
		}]
		input := url.Values{"batchId": {hikvisionProject}, "postAdSnList": {""}, "workPlaces": {""}, "interviewMethodList": {""}, "keyWord": {""}, "jobNature": {"应届生"}, "pageNum": {strconv.Itoa(page)}, "pageSize": {"50"}, "notZxfFlag": {"notZxfFlag"}}
		if err := a.post(ctx, s, hikvisionOrigin+"/api/search/crsPositionSearch/getPositionByQuery", input, &v); err != nil {
			return campusPage{}, err
		}
		if err := hikOK(v); err != nil {
			return campusPage{}, err
		}
		if v.Data.Total == nil || v.Data.Rows == nil || v.Data.Size != 50 {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: v.Data.Page, Size: v.Data.Size}
		for _, row := range v.Data.Rows {
			ref, err := hikRef(row)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchHikvision(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !hex32.MatchString(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	scope, err := a.hikScope(ctx, s)
	if err != nil {
		return "", err
	}
	var v hikEnvelope[hikPost]
	if err := a.get(ctx, s, hikvisionOrigin+"/api/userfacade/crsPublishPost/findId?id="+r.ExternalID, &v); err != nil {
		return "", err
	}
	if err := hikOK(v); err != nil {
		return "", err
	}
	ref, err := hikRef(v.Data)
	if err != nil || ref.ExternalID != r.ExternalID || ref.URL != r.URL {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	if v.Data.Merge != nil && *v.Data.Merge == 2 {
		var merged hikEnvelope[[]struct {
			hikPost
			Root       string `json:"mergeRootId"`
			Department string `json:"needDeptName"`
			Direction  string `json:"techDire"`
		}]
		if err := a.get(ctx, s, hikvisionOrigin+"/api/userfacade/crsPublishPost/findIdMerge?id="+r.ExternalID, &merged); err != nil {
			return "", err
		}
		if err := hikOK(merged); err != nil {
			return "", err
		}
		if len(merged.Data) == 0 || len(merged.Data) > 100 {
			return "", fail("SCHEMA_INVALID", false, 200)
		}
		parts := []string{}
		seen := map[string]bool{}
		for _, child := range merged.Data {
			if !hex32.MatchString(child.ID) || seen[child.ID] || child.Root != r.ExternalID || child.Batch != hikvisionProject || child.BatchName != scope.Name || child.Name != v.Data.Name || child.Nature != "校招应届生" || child.Active != 1 || child.Status != 1 || child.Merge == nil || *child.Merge != 1 {
				return "", fail("SCHEMA_INVALID", false, 200)
			}
			seen[child.ID] = true
			childRef := ref
			childRef.Locations = splitPlaces(child.Places)
			text, err := campusText(childRef, scope.Name+"；官网毕业范围："+scope.Remark+"；部门："+child.Department+"；方向："+child.Direction, child.Duties, child.Requirements)
			if err != nil {
				return "", err
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n\n"), nil
	}
	return campusText(ref, scope.Name+"；官网毕业范围："+scope.Remark, v.Data.Duties, v.Data.Requirements)
}
