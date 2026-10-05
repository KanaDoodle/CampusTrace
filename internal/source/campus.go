package source

import (
	"context"
	"net/url"
	"strings"
)

const BaiduCampusURL = "https://talent.baidu.com/jobs/list?recruitType=GRADUATE"
const MeituanCampusURL = "https://zhaopin.meituan.com/web/campus?hiringType=1_1"
const JDCampusURL = "https://campus.jd.com/#/jobs?type=present"
const NeteaseCampusURL = "https://campus.163.com/app/job/position?id=103"
const AlibabaCampusURL = "https://campus-talent.alibaba.com/campus/position?batchId=100000760001"
const BilibiliCampusURL = "https://jobs.bilibili.com/campus/positions?type=3"
const KuaishouCampusURL = "https://campus.kuaishou.cn/recruit/campus/e/#/campus/jobs?recruitSubProjectCodes=20271779425607"

// Presets describe implemented scopes, not promises about current availability.
type CampusSite struct {
	Adapter           string `json:"adapter"`
	Company           string `json:"company"`
	URL               string `json:"url"`
	Scope             string `json:"scope"`
	MinimumInterval   int    `json:"minimum_interval"`
	SupportsDirection bool   `json:"supports_direction"`
}

func CampusSites() []CampusSite {
	return []CampusSite{
		{"xiaohongshu", "小红书", XHSURL, "当前常规应届校招项目", 1800, true},
		{"baidu", "百度", BaiduCampusURL, "应届生校招（含 AIDU、管培生项目）", 1800, false},
		{"meituan", "美团", MeituanCampusURL, "应届生校招", 1800, false},
		{"jd", "京东", JDCampusURL, "应届生项目（JDS、TET、新锐之星）", 1800, false},
		{"netease", "网易互联网", NeteaseCampusURL, "2027 届校园招聘（不含互娱、雷火）", 1800, false},
		{"alibaba", "阿里巴巴", AlibabaCampusURL, "2027 届应届生（官网当前公开业务集团）", 1800, false},
		{"bilibili", "哔哩哔哩", BilibiliCampusURL, "官网公开应届生岗位（不含实习）", 1800, false},
		{"kuaishou", "快手", KuaishouCampusURL, "2027 届应届生（不含留用、日常实习）", 1800, false},
	}
}

func campusSite(raw string) (CampusSite, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawPath != "" {
		return CampusSite{}, fail("UNSUPPORTED", false, 0)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return CampusSite{}, fail("UNSUPPORTED", false, 0)
	}
	path := strings.TrimRight(u.Path, "/")
	for _, site := range CampusSites() {
		if site.Adapter == "kuaishou" && u.Host == "campus.kuaishou.cn" && path == "/recruit/campus/e" && u.RawQuery == "" && u.RawFragment == "" && (u.Fragment == "" || u.Fragment == "/campus/index" || u.Fragment == "/campus/jobs" || u.Fragment == "/campus/jobs?recruitSubProjectCodes="+kuaishouProjectCode) {
			return site, nil
		}
		if site.Adapter == "jd" && u.Host == "campus.jd.com" && path == "" && u.RawQuery == "" && (u.Fragment == "" || u.Fragment == "/jobs" || u.Fragment == "/jobs?type=present") {
			return site, nil
		}
		if u.Fragment != "" {
			continue
		}
		switch site.Adapter {
		case "xiaohongshu":
			if u.Host == "job.xiaohongshu.com" && path == "/campus/position" && u.RawQuery == "" {
				return site, nil
			}
		case "baidu":
			if u.Host == "talent.baidu.com" && (path == "/jobs/list" || path == "/jobs/campus") && (u.RawQuery == "" || (len(query) == 1 && len(query["recruitType"]) == 1 && query.Get("recruitType") == "GRADUATE")) {
				return site, nil
			}
		case "meituan":
			if u.Host == "zhaopin.meituan.com" && path == "/web/campus" && (u.RawQuery == "" || (len(query) == 1 && len(query["hiringType"]) == 1 && query.Get("hiringType") == "1_1")) {
				return site, nil
			}
		case "netease":
			if u.Host == "campus.163.com" && path == "/app/job/position" && len(query) == 1 && len(query["id"]) == 1 && query.Get("id") == "103" {
				return site, nil
			}
		case "alibaba":
			if u.Host == "campus-talent.alibaba.com" && path == "/campus/position" && len(query) == 1 && len(query["batchId"]) == 1 && query.Get("batchId") == "100000760001" {
				return site, nil
			}
		case "bilibili":
			if u.Host == "jobs.bilibili.com" && path == "/campus/positions" && (u.RawQuery == "" || (len(query) == 1 && len(query["type"]) == 1 && query.Get("type") == "3")) {
				return site, nil
			}
		}
	}
	return CampusSite{}, fail("UNSUPPORTED", false, 0)
}

func RecognizeCampusURL(raw string) error {
	_, err := campusSite(raw)
	return err
}

func (a PublicPlatform) PreviewCampus(ctx context.Context, raw string) (CampusPreview, error) {
	site, err := campusSite(raw)
	if err != nil {
		return CampusPreview{}, err
	}
	var v CampusPreview
	if site.Adapter == "xiaohongshu" {
		v, err = a.PreviewXHS(ctx, raw)
		v.Name = site.Company + " · " + v.Name
	} else if site.Adapter == "alibaba" {
		v, err = a.previewAlibaba(ctx, site)
	} else if site.Adapter == "bilibili" {
		v, err = a.previewBilibili(ctx, site)
	} else if site.Adapter == "kuaishou" {
		v, err = a.previewKuaishou(ctx, site)
	} else if site.Adapter == "jd" || site.Adapter == "netease" {
		v, err = a.previewPortal(ctx, site)
	} else {
		v, err = a.previewGraduate(ctx, site)
	}
	v.Adapter, v.Company, v.MinimumInterval, v.SupportsDirection = site.Adapter, site.Company, site.MinimumInterval, site.SupportsDirection
	return v, err
}
