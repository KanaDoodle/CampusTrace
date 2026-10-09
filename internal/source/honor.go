package source

import (
	"context"
	"net/url"
	"strconv"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const honorOrigin = "https://career.honor.com"
const honorSuite = "SU60eea919bef57c1023f6fe78"
const honorProject = "101801"
const HonorCampusURL = honorOrigin + "/" + honorSuite + "/pb/school.html"

type honorEnvelope[T any] struct {
	State string `json:"state"`
	Type  string `json:"type"`
	Data  T      `json:"data"`
}
type honorPost struct {
	ID           string `json:"postId"`
	Name         string `json:"postName"`
	Recruit      *int   `json:"recruitType"`
	Project      int    `json:"projectId"`
	ProjectName  string `json:"projectName"`
	WorkType     string `json:"workTypeStr"`
	Place        string `json:"workPlaceStr"`
	Duties       string `json:"workContent"`
	Requirements string `json:"serviceCondition"`
	Deadline     string `json:"endDate"`
	Education    string `json:"educationStr"`
	Subject      string `json:"subject"`
}

func honorOK[T any](v honorEnvelope[T]) error {
	if v.State != "200" || v.Type != "success" {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func honorURL(id string) string {
	return honorOrigin + "/" + honorSuite + "/pb/posDetail.html?postId=" + id + "&postType=campus"
}
func honorRef(row honorPost) (PostingRef, error) {
	if !siemensID.MatchString(row.ID) || row.Recruit == nil || *row.Recruit != 1 || row.Project != 101801 || row.ProjectName != "2027届应届本硕" || row.WorkType != "全职" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	places := splitPlaces(row.Place)
	if row.Place == "全部地区" {
		places = []string{}
	}
	ref := PostingRef{ExternalID: row.ID, URL: honorURL(row.ID), Title: row.Name, Company: "荣耀", JobType: "FULL_TIME", Locations: places}
	return ref, validateRef(ref)
}
func honorEndpoint(name string) string {
	return honorOrigin + "/wecruit/positionInfo/" + name + "/" + honorSuite + "?iSaJAx=isAjax&request_locale=zh_CN"
}
func (a PublicPlatform) discoverHonor(ctx context.Context, s d.Source) ([]PostingRef, error) {
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v honorEnvelope[struct {
			Form struct {
				Rows  []honorPost `json:"pageData"`
				Total *int        `json:"dataCount"`
				Page  int         `json:"currentPage"`
				Size  int         `json:"pageSize"`
				Pages int         `json:"totalPage"`
			} `json:"pageForm"`
		}]
		input := url.Values{"isFrompb": {"true"}, "recruitType": {"1"}, "projectCode": {honorProject}, "pageSize": {"15"}, "currentPage": {strconv.Itoa(page)}}
		if err := a.post(ctx, s, honorEndpoint("listPosition"), input, &v); err != nil {
			return campusPage{}, err
		}
		if err := honorOK(v); err != nil {
			return campusPage{}, err
		}
		f := v.Data.Form
		if f.Total == nil || f.Rows == nil || f.Size != 15 || f.Pages != (*f.Total+14)/15 {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *f.Total, Page: f.Page, Size: f.Size}
		for _, row := range f.Rows {
			ref, err := honorRef(row)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchHonor(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !siemensID.MatchString(r.ExternalID) || r.URL != honorURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	var v honorEnvelope[honorPost]
	if err := a.post(ctx, s, honorEndpoint("listPositionDetail"), url.Values{"postId": {r.ExternalID}, "recruitType": {"1"}}, &v); err != nil {
		return "", err
	}
	if err := honorOK(v); err != nil {
		return "", err
	}
	ref, err := honorRef(v.Data)
	if err != nil || ref.ExternalID != r.ExternalID {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	// canDelivery is candidate-dependent even on anonymous reads; never use it as
	// an authoritative open/closed flag or applicant eligibility decision.
	text, err := campusText(ref, v.Data.ProjectName, v.Data.Duties, v.Data.Requirements)
	if err == nil && v.Data.Deadline != "" {
		text += "\n官网公布截止时间：" + v.Data.Deadline
	}
	return text, err
}
