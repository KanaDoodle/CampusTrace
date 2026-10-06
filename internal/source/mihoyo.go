package source

import (
	"context"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const MihoyoCampusURL = "https://jobs.mihoyo.com/campus/position"
const mihoyoAPI = "https://ats.openout.mihoyo.com/ats-portal"
const mihoyoProjectName = "2027届秋招"
const mihoyoTarget = "2027届（2026.9-2027.8之间毕业）"

type mihoyoEnvelope[T any] struct {
	Code    *int `json:"code"`
	Success bool `json:"success"`
	Data    T    `json:"data"`
}

func (v mihoyoEnvelope[T]) valid() bool { return v.Code != nil && *v.Code == 0 && v.Success }

type mihoyoJob struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Addresses []struct {
		Name string `json:"addressDetail"`
	} `json:"addressDetailList"`
	Kind         *int   `json:"jobNatureId"`
	KindName     string `json:"jobNature"`
	ProjectName  string `json:"projectName"`
	Channels     []int  `json:"channelDetailIds"`
	ObjectID     string `json:"objectId"`
	ObjectName   string `json:"objectName"`
	Project      *int   `json:"projectId"`
	HireType     *int   `json:"hireType"`
	Status       *int   `json:"status"`
	Duty         string `json:"description"`
	Require      string `json:"jobRequire"`
	Bonus        string `json:"addition"`
	Instructions string `json:"deliveryInstructions"`
}

func mihoyoRef(v mihoyoJob) (PostingRef, error) {
	public := false
	for _, c := range v.Channels {
		public = public || c == 1
	}
	// A few jobs in the verified graduate project omit the optional graduate
	// window. Preserve the absence; reject any conflicting or partial window.
	targetValid := (v.ObjectID == "" && v.ObjectName == "") || (v.ObjectID == "19" && v.ObjectName == mihoyoTarget)
	if !numericID(v.ID) || v.Kind == nil || *v.Kind != 1 || v.KindName != "全职" || v.ProjectName != mihoyoProjectName || !targetValid || !public {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	places := []string{}
	for _, address := range v.Addresses {
		places = append(places, address.Name)
	}
	r := PostingRef{ExternalID: v.ID, URL: MihoyoCampusURL + "/" + v.ID, Title: v.Title, Company: "米哈游", JobType: "FULL_TIME", Locations: splitPlaces(strings.Join(places, "、"))}
	return r, validateRef(r)
}
func (a PublicPlatform) discoverMihoyo(ctx context.Context, s d.Source) ([]PostingRef, error) {
	var project mihoyoEnvelope[[]struct {
		IDs   []int  `json:"projectIdList"`
		Name  string `json:"projectName"`
		Count *int   `json:"count"`
	}]
	if err := a.post(ctx, s, mihoyoAPI+"/v1/job/project_count/list", map[string]any{"channelDetailIds": []int{1}, "hireType": 1}, &project); err != nil {
		return nil, err
	}
	count, matches := -1, 0
	for _, p := range project.Data {
		if p.Name == "应届生投递" && len(p.IDs) == 1 && p.IDs[0] == 13 && p.Count != nil {
			count, matches = *p.Count, matches+1
		}
	}
	if !project.valid() || matches != 1 || count < 0 {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v mihoyoEnvelope[struct {
			Jobs  []mihoyoJob `json:"list"`
			Page  int         `json:"pageNo"`
			Size  int         `json:"pageSize"`
			Total *int        `json:"total"`
		}]
		if err := a.post(ctx, s, mihoyoAPI+"/v1/job/list", map[string]any{"channelDetailIds": []int{1}, "hireType": 1, "projectIds": []int{13}, "pageNo": page, "pageSize": 50}, &v); err != nil {
			return campusPage{}, err
		}
		if !v.valid() || v.Data.Total == nil || *v.Data.Total != count || v.Data.Page != page || v.Data.Size != 50 || v.Data.Jobs == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 50}
		for _, j := range v.Data.Jobs {
			r, err := mihoyoRef(j)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchMihoyo(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != MihoyoCampusURL+"/"+r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v mihoyoEnvelope[mihoyoJob]
	if err := a.post(ctx, s, mihoyoAPI+"/v1/job/info", map[string]any{"id": r.ExternalID, "channelDetailIds": []int{1}, "hireType": 1}, &v); err != nil {
		return "", err
	}
	j := v.Data
	actual, err := mihoyoRef(j)
	if err != nil {
		return "", err
	}
	if !v.valid() || j.ID != r.ExternalID || j.Title != r.Title || j.Project == nil || *j.Project != 13 || j.HireType == nil || *j.HireType != 1 || j.Status == nil || *j.Status != 1 {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text, err := campusText(actual, mihoyoProjectName, j.Duty, j.Require)
	if err != nil {
		return "", err
	}
	if j.ObjectName != "" {
		text += "\n毕业范围（官网原文）：" + j.ObjectName
	}
	if bonus := plainHTML(j.Bonus); strings.TrimSpace(bonus) != "" {
		text += "\n加分项（官网原文）：\n" + bonus
	}
	if instructions := plainHTML(j.Instructions); strings.TrimSpace(instructions) != "" {
		text += "\n投递说明（官网原文）：\n" + instructions
	}
	return text, nil
}
