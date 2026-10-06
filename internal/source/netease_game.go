package source

import (
	"context"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"net/url"
	"strconv"
	"strings"
)

const neteaseGameOrigin = "https://campus.game.163.com"

func neteaseGameURL(id string) string {
	return neteaseGameOrigin + "/app/detail/index?id=" + id + "&projectId=102"
}
func neteaseGameRef(v neteasePost) (PostingRef, error) {
	if v.ID <= 0 || v.ProjectID != 102 {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	id := strconv.FormatInt(v.ID, 10)
	r := PostingRef{ExternalID: id, URL: neteaseGameURL(id), Title: v.Name, Company: "网易游戏互娱", JobType: "FULL_TIME", Locations: splitPlaces(v.Place)}
	return r, validateRef(r)
}
func (a PublicPlatform) verifyNeteaseGame(ctx context.Context, s d.Source) error {
	var v neteaseEnvelope[[]struct {
		Title    string `json:"title"`
		Children []struct {
			Title string `json:"title"`
			Link  string `json:"link"`
		} `json:"children"`
	}]
	if err := a.get(ctx, s, neteaseGameOrigin+"/api/campuspc/project/navigation/list", &v); err != nil {
		return err
	}
	if v.Code != 200 || v.Data == nil {
		return fail("SCHEMA_INVALID", false, 200)
	}
	n := 0
	for _, parent := range v.Data {
		if parent.Title == "应届生" {
			for _, p := range parent.Children {
				if p.Link == NeteaseGameCampusURL && p.Title == "网易互娱2027届校园招聘" {
					n++
				}
			}
		}
	}
	if n != 1 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}
func (a PublicPlatform) neteaseGameRows(ctx context.Context, s d.Source, page int, id string) ([]neteasePost, int, error) {
	var v neteaseEnvelope[struct {
		Total *int          `json:"total"`
		Pages int           `json:"pages"`
		Jobs  []neteasePost `json:"list"`
	}]
	q := url.Values{"projectId": {"102"}, "currentPage": {strconv.Itoa(page)}, "pageSize": {"50"}}
	if id != "" {
		q.Set("positionIdList", id)
	}
	if err := a.get(ctx, s, neteaseGameOrigin+"/api/campuspc/position/getJobList?"+q.Encode(), &v); err != nil {
		return nil, 0, err
	}
	if v.Code != 200 || v.Data.Total == nil || v.Data.Jobs == nil || v.Data.Pages != (*v.Data.Total+49)/50 {
		return nil, 0, fail("SCHEMA_INVALID", false, 200)
	}
	return v.Data.Jobs, *v.Data.Total, nil
}
func (a PublicPlatform) discoverNeteaseGame(ctx context.Context, s d.Source) ([]PostingRef, error) {
	if err := a.verifyNeteaseGame(ctx, s); err != nil {
		return nil, err
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		rows, total, err := a.neteaseGameRows(ctx, s, page, "")
		if err != nil {
			return campusPage{}, err
		}
		out := campusPage{Refs: []PostingRef{}, Total: total, Page: page, Size: 50}
		for _, j := range rows {
			r, err := neteaseGameRef(j)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, r)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchNeteaseGame(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != neteaseGameURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	if err := a.verifyNeteaseGame(ctx, s); err != nil {
		return "", err
	}
	rows, total, err := a.neteaseGameRows(ctx, s, 1, r.ExternalID)
	if err != nil {
		return "", err
	}
	if total != 1 || len(rows) != 1 {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	actual, err := neteaseGameRef(rows[0])
	if err != nil || actual.ExternalID != r.ExternalID || actual.Title != r.Title {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return campusText(actual, "网易互娱2027届校园招聘（独立于网易互联网、雷火）", rows[0].Duty, rows[0].Requirement)
}

// Keep the two independent NetEase ATS namespaces and original URLs distinct.
func validLeihuoLink(raw, id string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "campus.163.com" || u.User != nil || u.Fragment != "" || u.RawPath != "" || u.Path != "/app/detail/index" {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	return err == nil && len(q) == 2 && len(q["id"]) == 1 && len(q["projectId"]) == 1 && q.Get("id") == id && q.Get("projectId") == "77" && !strings.Contains(raw, "#")
}
