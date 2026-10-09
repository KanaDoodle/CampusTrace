package source

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const SangforCampusURL = "https://hr.sangfor.com/campucompon/schoolRecruitment"
const sangforOrigin = "https://hr.sangfor.com"
const sangforAPI = sangforOrigin + "/api/api"
const sangforScope = "2027_regular_101"

type sangforEnvelope[T any] struct {
	Code *int `json:"code"`
	Data T    `json:"data"`
}
type sangforPost struct {
	ID          int64  `json:"positionId"`
	Channels    []int  `json:"channelIds"`
	Title       string `json:"title"`
	State       string `json:"positionState"`
	Description string `json:"description"`
	Commitment  string `json:"commitment"`
	Education   string `json:"education"`
	Place       string `json:"workPlaceText"`
}

func sangforOK[T any](v sangforEnvelope[T]) error {
	if v.Code != nil && (*v.Code == 401 || *v.Code == 403 || *v.Code == 1001) {
		return fail("BLOCKED", false, 200)
	}
	if v.Code == nil || *v.Code != 0 {
		return fail("SCHEMA_INVALID", false, 200)
	}
	return nil
}

// The public client bootstraps an anonymous visitor without candidate login.
// The token is operation-local, never persisted or placed in the HTTP cache.
func (a PublicPlatform) sangforSession(ctx context.Context, s d.Source) (PublicPlatform, error) {
	a.sangforToken = ""
	bootstrap := a
	bootstrap.CacheRead, bootstrap.CacheWrite = nil, nil
	var v struct {
		Token string `json:"access_token"`
		Type  string `json:"token_type"`
	}
	if err := bootstrap.get(ctx, s, sangforAPI+"/connect/token", &v); err != nil {
		return a, err
	}
	if v.Type != "Bearer" || len(v.Token) == 0 || len(v.Token) > 8192 || strings.ContainsAny(v.Token, "\r\n") {
		return a, fail("SCHEMA_INVALID", false, 200)
	}
	a.sangforToken = v.Token
	return a, nil
}

func sangforJobURL(id string) string {
	return sangforOrigin + "/campucompon/Delivery/" + id
}
func sangforRef(row sangforPost) (PostingRef, error) {
	regular := false
	for _, id := range row.Channels {
		if id == 101 {
			regular = true
		}
		if id == 102 || id == 121 {
			return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
		}
	}
	if row.ID <= 0 || !regular || row.State != "open" || row.Commitment != "全职" {
		return PostingRef{}, fail("SCHEMA_INVALID", false, 200)
	}
	id := strconv.FormatInt(row.ID, 10)
	ref := PostingRef{ExternalID: id, URL: sangforJobURL(id), Company: "深信服", Title: row.Title, JobType: "FULL_TIME", Locations: splitPlaces(row.Place)}
	return ref, validateRef(ref)
}
func (a PublicPlatform) discoverSangfor(ctx context.Context, s d.Source) ([]PostingRef, error) {
	a, err := a.sangforSession(ctx, s)
	if err != nil {
		return nil, err
	}
	var channels sangforEnvelope[[]struct {
		ID     int    `json:"channelId"`
		Name   string `json:"channelName"`
		Parent int    `json:"parentId"`
	}]
	if err := a.get(ctx, s, sangforAPI+"/Jobs/channel/100", &channels); err != nil {
		return nil, err
	}
	if err := sangforOK(channels); err != nil {
		return nil, err
	}
	matched := 0
	for _, channel := range channels.Data {
		if channel.ID == 101 {
			if channel.Name != "27届校园招聘" || channel.Parent != 100 {
				return nil, fail("SCHEMA_INVALID", false, 200)
			}
			matched++
		}
	}
	if matched != 1 {
		return nil, fail("SCHEMA_INVALID", false, 200)
	}
	return campusPages(ctx, func(ctx context.Context, page int) (campusPage, error) {
		var v sangforEnvelope[struct {
			Count *int          `json:"count"`
			Rows  []sangforPost `json:"listData"`
		}]
		input := map[string]any{"channelId": 101, "page": page, "pageSize": 10, "departmentId": 0, "functionId": 0, "kw": "", "locationId": 0, "workPlaceId": 0}
		if err := a.post(ctx, s, sangforAPI+"/Jobs", input, &v); err != nil {
			return campusPage{}, err
		}
		if err := sangforOK(v); err != nil {
			return campusPage{}, err
		}
		if v.Data.Count == nil || v.Data.Rows == nil {
			return campusPage{}, fail("SCHEMA_INVALID", false, 200)
		}
		out := campusPage{Refs: []PostingRef{}, Total: *v.Data.Count, Page: page, Size: 10}
		for _, row := range v.Data.Rows {
			ref, err := sangforRef(row)
			if err != nil {
				return out, err
			}
			out.Refs = append(out.Refs, ref)
		}
		return out, nil
	})
}
func (a PublicPlatform) fetchSangfor(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	if !numericID(r.ExternalID) || r.URL != sangforJobURL(r.ExternalID) {
		return "", fail("SCHEMA_INVALID", false, 0)
	}
	a, err := a.sangforSession(ctx, s)
	if err != nil {
		return "", err
	}
	var v sangforEnvelope[struct {
		Row sangforPost `json:"listData"`
	}]
	if err := a.get(ctx, s, sangforAPI+"/Jobs/"+r.ExternalID, &v); err != nil {
		return "", err
	}
	if err := sangforOK(v); err != nil {
		return "", err
	}
	actual, err := sangforRef(v.Data.Row)
	text := plainHTML(v.Data.Row.Description)
	if err != nil || actual.ExternalID != r.ExternalID || actual.Title != r.Title || actual.Company != r.Company || strings.TrimSpace(text) == "" {
		return "", fail("SCHEMA_INVALID", false, 200)
	}
	return fmt.Sprintf("岗位名称：%s\n公司：%s\n招聘类型：应届生全职\n招聘范围：2027 届常规校园招聘（具体毕业时间见原文）\n工作地点：%s\n官网学历要求：%s\n岗位原文：\n%s", actual.Title, actual.Company, strings.Join(actual.Locations, "、"), v.Data.Row.Education, text), nil
}
