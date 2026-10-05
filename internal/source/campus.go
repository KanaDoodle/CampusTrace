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
const SiemensCampusURL = "https://jobs.siemens.com.cn/siemens/position/index?recruitmentType=CAMPUSRECRUITMENT"
const HaierCampusURL = "https://maker.haier.net/client/campusmobile/activity/id/68/fid.html"
const OPPOCampusURL = "https://careers.oppo.com/university/oppo/campus/post?recruitType=Graduate"
const SGMCampusURL = "https://sgm.zhiye.com/campusjobs"
const CtripCampusURL = "https://careers.ctrip.com/campus/jobList"
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
		{"siemens", "西门子", SiemensCampusURL, "中国官网校招分类（不含实习、社招）", 1800, false},
		{"haier", "海尔集团", HaierCampusURL, "2027 校园招聘项目", 1800, false},
		{"oppo", "OPPO", OPPOCampusURL, "2027 届应届生（不含实习、博士专项）", 1800, false},
		{"kuaishou", "快手", KuaishouCampusURL, "2027 届应届生（不含留用、日常实习）", 1800, false},
		{"qihoo360", "360集团", "https://360campus.zhiye.com/campus/jobs", "官网校招分类（具体毕业年份见岗位原文）", 1800, false},
		{"sany", "三一集团", "https://sanycampus.zhiye.com/campus/jobs", "官网校招分类（不含社招、实习）", 1800, false},
		{"inovance", "汇川技术", "https://inovance.zhiye.com/campus/jobs", "官网校招分类（当前可能暂未开放岗位）", 1800, false},
		{"vivo", "vivo", "https://hr-campus.vivo.com/campus/jobs", "官网校招分类（不含社招、实习）", 1800, false},
		{"sgm", "上汽通用/泛亚", SGMCampusURL, "官网校招分类（当前含 2027 届秋招项目；具体届别以岗位原文为准）", 1800, false},
		{"honor", "荣耀", HonorCampusURL, "2027 届应届本硕（不含博士专项、实习）", 1800, false},
		{"lenovo", "联想", LenovoCampusURL, "官网应届生招聘（不含人才专项、实习）", 1800, false},
		{"midea", "美的集团", MideaCampusURL, "2027 届美的星校招（不含博士专项、实习）", 1800, false},
		{"byd", "比亚迪", BYDCampusURL, "2027 应届生（不含实习、外派专项）", 1800, false},
		{"hikvision", "海康威视", HikvisionCampusURL, "2027 常规校园招聘（不含智先锋、实习）", 1800, false},
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
		if site.Adapter == "lenovo" && u.Host == "talent.lenovo.com.cn" && path == "" && u.RawQuery == "" && u.RawFragment == "" && (u.Fragment == "" || u.Fragment == "/campus") {
			return site, nil
		}
		if site.Adapter == "kuaishou" && u.Host == "campus.kuaishou.cn" && path == "/recruit/campus/e" && u.RawQuery == "" && u.RawFragment == "" && (u.Fragment == "" || u.Fragment == "/campus/index" || u.Fragment == "/campus/jobs" || u.Fragment == "/campus/jobs?recruitSubProjectCodes="+kuaishouProjectCode) {
			return site, nil
		}
		if site.Adapter == "jd" && u.Host == "campus.jd.com" && path == "" && u.RawQuery == "" && (u.Fragment == "" || u.Fragment == "/jobs" || u.Fragment == "/jobs?type=present") {
			return site, nil
		}
		if u.Fragment != "" {
			continue
		}
		if cfg, ok := beisenCompanies[site.Adapter]; ok && u.Host == strings.TrimPrefix(cfg.Origin, "https://") && (path == "" || path == "/campus" || path == "/campus/jobs" || (site.Adapter == "sgm" && path == "/campusjobs")) && u.RawQuery == "" {
			return site, nil
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
		case "siemens":
			if u.Host == "jobs.siemens.com.cn" && path == "/siemens/position/index" && len(query) == 1 && len(query["recruitmentType"]) == 1 && query.Get("recruitmentType") == siemensScope {
				return site, nil
			}
		case "haier":
			if u.Host == "maker.haier.net" && path == "/client/campusmobile/activity/id/68/fid.html" && u.RawQuery == "" {
				return site, nil
			}
		case "oppo":
			if u.Host == "careers.oppo.com" && path == "/university/oppo/campus/post" && (u.RawQuery == "" || (len(query) == 1 && len(query["recruitType"]) == 1 && query.Get("recruitType") == "Graduate")) {
				return site, nil
			}
		case "honor":
			if u.Host == "career.honor.com" && path == "/"+honorSuite+"/pb/school.html" && u.RawQuery == "" {
				return site, nil
			}
		case "midea":
			if u.Host == "careers.midea.com" && (path == "/schoolOut" && u.RawQuery == "" || path == "/schoolOut/post" && len(query) == 1 && len(query["projectType"]) == 1 && query.Get("projectType") == "1") {
				return site, nil
			}
		case "byd":
			if u.Host == "job.byd.com" && (path == "/portal/mobile/school-home" && u.RawQuery == "" || path == "/portal/mobile/schoolPositionList" && len(query) == 1 && len(query["tab"]) == 1 && query.Get("tab") == "应届生") {
				return site, nil
			}
		case "hikvision":
			if u.Host == "campushr.hikvision.com" && path == "/school" && len(query) == 1 && len(query["schoolType"]) == 1 && query.Get("schoolType") == "nozxf" {
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
	} else if site.Adapter == "siemens" {
		v, err = a.previewSiemens(ctx, site)
	} else if site.Adapter == "haier" {
		v, err = a.previewHaier(ctx, site)
	} else if site.Adapter == "oppo" {
		v, err = a.previewOPPO(ctx, site)
	} else if site.Adapter == "kuaishou" {
		v, err = a.previewKuaishou(ctx, site)
	} else if moreTenant(site.Adapter) != "" {
		v, err = a.previewMore(ctx, site)
	} else if site.Adapter == "jd" || site.Adapter == "netease" {
		v, err = a.previewPortal(ctx, site)
	} else {
		v, err = a.previewGraduate(ctx, site)
	}
	v.Adapter, v.Company, v.MinimumInterval, v.SupportsDirection = site.Adapter, site.Company, site.MinimumInterval, site.SupportsDirection
	return v, err
}
