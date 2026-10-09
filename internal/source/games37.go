package source

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const games37Origin = "https://zhaopin.37.com"
const Games37CampusURL = games37Origin + "/index.php?m=Home&c=recruit&a=recruit&recruit=3"
const games37JobPrefix = "https://app.mokahr.com/campus_apply/37/25238#/job/"

type games37Post struct {
	URL      string `json:"url"`
	Name     string `json:"name"`
	Places   string `json:"work_place"`
	Kind     string `json:"pname"`
	Text     string `json:"duty"`
	Category string `json:"cate_name"`
}
type games37Envelope struct {
	Code *int `json:"code"`
	Data struct {
		Rows  []games37Post `json:"list"`
		Total *int          `json:"count"`
		Page  int           `json:"page"`
		Size  int           `json:"pageSize"`
		Pages int           `json:"pageCount"`
	} `json:"data"`
}

func games37Ref(row games37Post) (PostingRef, error) {
	id := strings.TrimPrefix(row.URL, games37JobPrefix)
	if row.Category != "校园招聘" || !baiduPostID.MatchString(id) || row.URL != games37JobPrefix+id {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	ref := PostingRef{ExternalID: id, URL: row.URL, Title: row.Name, Company: "三七互娱", JobType: "UNKNOWN", Locations: splitPlaces(row.Places)}
	return ref, validateRef(ref)
}

// The official read-only campus list includes the complete JD and its Moka
// application URL. Read the published text here; no Moka verification is bypassed.
func (a PublicPlatform) loadGames37(ctx context.Context, s d.Source, keyword string) ([]PostingRef, map[string]games37Post, error) {
	posts := map[string]games37Post{}
	refs, err := campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		q := url.Values{"m": {"Home"}, "c": {"campus"}, "a": {"getIndexPage"}, "key": {keyword}, "post_type": {""}, "place_type": {""}, "page": {strconv.Itoa(page)}}
		var v games37Envelope
		if err := a.get(ctx, s, games37Origin+"/index.php?"+q.Encode(), &v); err != nil {
			return campusPage{}, err
		}
		if v.Code == nil || *v.Code != 0 || v.Data.Total == nil || v.Data.Rows == nil || v.Data.Size <= 0 || v.Data.Pages != (*v.Data.Total+v.Data.Size-1)/v.Data.Size {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Total, Page: v.Data.Page, Size: v.Data.Size}
		for _, row := range v.Data.Rows {
			ref, err := games37Ref(row)
			if err != nil {
				return campusPage{}, err
			}
			if _, err := games37Text(ref, row); err != nil {
				return campusPage{}, err
			}
			posts[ref.ExternalID] = row
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return refs, posts, nil
}

func games37Text(ref PostingRef, row games37Post) (string, error) {
	// Preserve the company's full wording instead of splitting its sentences.
	if !strings.Contains(row.Text, "【岗位职责】") || !strings.Contains(row.Text, "【任职要求】") {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	parts := strings.Split(row.Text, "【任职要求】")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "【岗位职责】") {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	text, err := bankPostingText(ref, "官网校园招聘分类（具体毕业年份及用工形式以原文为准）", strings.TrimPrefix(parts[0], "【岗位职责】"), parts[1])
	if err != nil {
		return "", err
	}
	if row.Kind != "" {
		text += "\n官网职位类别：" + row.Kind
	}
	return text, nil
}
func (a PublicPlatform) discoverGames37(ctx context.Context, s d.Source) ([]PostingRef, error) {
	refs, _, err := a.loadGames37(ctx, s, "")
	return refs, err
}
func (a PublicPlatform) fetchGames37(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !baiduPostID.MatchString(r.ExternalID) || r.URL != games37JobPrefix+r.ExternalID || r.Company != "三七互娱" {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	words := strings.Fields(r.Title)
	if len(words) == 0 {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	// The published search does not match some titles containing spaces. Use
	// its first word and still bind by UUID; fall back to the full campus list
	// only when the query fails to find that UUID.
	_, posts, err := a.loadGames37(ctx, s, words[0])
	if err != nil {
		return "", err
	}
	row, ok := posts[r.ExternalID]
	if !ok {
		_, posts, err = a.loadGames37(ctx, s, "")
		if err != nil {
			return "", err
		}
		row, ok = posts[r.ExternalID]
	}
	if !ok {
		return "", fail("HTTP_ERROR", false, 404)
	}
	if row.Name != r.Title || row.URL != r.URL {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	ref, err := games37Ref(row)
	if err != nil {
		return "", err
	}
	return games37Text(ref, row)
}
