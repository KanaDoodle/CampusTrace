package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"strconv"
)

const leihuoOrigin = "https://xiaozhao.leihuo.netease.com"

type leihuoEnvelope[T any] struct {
	Status int `json:"status"`
	Data   T   `json:"data"`
}
type leihuoJob struct {
	ID          string   `json:"ehr_job_id"`
	Project     string   `json:"ehr_project_id"`
	Kind        string   `json:"ehr_job_type"`
	KindName    string   `json:"type_name"`
	Target      string   `json:"target"`
	Name        string   `json:"job_name"`
	City        string   `json:"work_place_name"`
	URL         string   `json:"job_detail_url"`
	Duty        string   `json:"job_description"`
	Require     string   `json:"job_requirement"`
	Departments []string `json:"department_name"`
}

func leihuoRef(v leihuoJob) (PostingRef, error) {
	// The public full-time project also contains an explicit "27届及之后毕业"
	// target. Preserve the role-specific original target in the detail text.
	if !numericID(v.ID) || v.Project != "77" || v.Kind != "1" || v.KindName != "全职" || (v.Target != "2027届应届毕业生" && v.Target != "27届及之后毕业") || !validLeihuoLink(v.URL, v.ID) {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	r := PostingRef{ExternalID: v.ID, URL: v.URL, Title: v.Name, Company: "网易游戏雷火", JobType: "FULL_TIME", Locations: splitPlaces(v.City)}
	return r, validateRef(r)
}
func (a PublicPlatform) discoverLeihuo(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v leihuoEnvelope[struct {
			Total *int        `json:"count_number"`
			Pages int         `json:"pages_count"`
			Last  *bool       `json:"last_page"`
			Jobs  []leihuoJob `json:"apply_job_list"`
		}]
		q := url.Values{"project_id": {"77"}, "page_size": {"50"}, "page_number": {strconv.Itoa(page)}}
		if err := a.get(ctx, s, leihuoOrigin+"/api/apply/job/list/show?"+q.Encode(), &v); err != nil {
			return campusPage{}, err
		}
		if v.Status != 200 || v.Data.Total == nil || v.Data.Jobs == nil || v.Data.Last == nil || v.Data.Pages != (*v.Data.Total+49)/50 || *v.Data.Last != (page >= max(1, v.Data.Pages)) {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: page, Size: 50}
		for _, j := range v.Data.Jobs {
			r, err := leihuoRef(j)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchLeihuo(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || !validLeihuoLink(r.URL, r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v leihuoEnvelope[leihuoJob]
	q := url.Values{"job_id": {r.ExternalID}, "project_id": {"77"}}
	if err := a.get(ctx, s, leihuoOrigin+"/api/apply/job/detail/show?"+q.Encode(), &v); err != nil {
		return "", err
	}
	actual, err := leihuoRef(v.Data)
	if v.Status != 200 || err != nil || actual.ExternalID != r.ExternalID || actual.Title != r.Title {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return campusText(actual, "网易游戏雷火2027届应届生项目；官网招聘对象："+v.Data.Target+"（不含研究型、转正或日常实习项目）", v.Data.Duty, v.Data.Require)
}
