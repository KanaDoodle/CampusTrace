package source

import (
	"context"
	"net/url"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

const THSCampusURL = "https://campus.10jqka.com.cn/job/list?sid=61"
const CMBNTCampusURL = "https://cmbntjob.cmbchina.com/pages/schoolRecruit/index.html"
const NeteaseGameCampusURL = "https://campus.game.163.com/app/job/position?id=102"
const LeihuoCampusURL = "https://leihuo.163.com/campus/#/full"

var sectorScopes = map[string]struct{ Tenant, Origin string }{
	"yonyou":            {"106301", "https://career.yonyou.com"},
	"kedacom":           {"campus_2", kedacomOrigin},
	"games37":           {"campus", games37Origin},
	"sangfor":           {sangforScope, sangforOrigin},
	"bankcomm_tech":     {"campus_head_it", bankcommOrigin},
	"cms_securities":    {"101501", hotjobOrigin},
	"htsc_securities":   {"107301", hotjobOrigin},
	"cmb_tech":          {"graduate_tech", cmbOrigin},
	"citic_tech":        {"campus_it", citicOrigin},
	"boc_software":      {"2027_software", bocOrigin},
	"boc_operations":    {"2027_operations", bocOrigin},
	"ths":               {"61", "https://campus.10jqka.com.cn"},
	"cmbnt":             {"graduate", "https://cmbntjob.cmbchina.com"},
	"netease_game":      {"102", "https://campus.game.163.com"},
	"leihuo":            {"77", "https://xiaozhao.leihuo.netease.com"},
	"ctyun":             {"101101_581854", "https://job.chinatelecom.com.cn"},
	"ctcloud":           {"101101_581851", "https://job.chinatelecom.com.cn"},
	"mihoyo":            {"13", "https://ats.openout.mihoyo.com"},
	"pingan_tech":       {"graduate_PA011", pinganOrigin},
	"pingan_oneconnect": {"graduate_PA038", pinganOrigin},
	"pingan_wallet":     {"graduate_PA027", pinganOrigin},
	"cmcloud":           {"79", mobileOrigin},
	"cmiot":             {"77", mobileOrigin},
	"cmhome":            {"81", mobileOrigin},
	"gbits":             {gbitsProject, gbitsOrigin},
	"tcl_digital":       {tclProject + "_101206", tclOrigin},
	"tcl_honghu":        {tclProject + "_364906", tclOrigin},
	"cec_software":      {cecScope, cecOrigin},
}

func sectorEntry(site CampusSite, u *url.URL) bool {
	if site.Adapter == "kedacom" {
		return u.Host == "kedacom.zhiye.com" && strings.EqualFold(strings.TrimRight(u.Path, "/"), "/campus") && u.Fragment == "" && (u.RawQuery == "" || u.RawQuery == "r=2")
	}
	if site.Adapter == "games37" {
		return u.String() == Games37CampusURL
	}
	if site.Adapter == "sangfor" {
		return u.String() == SangforCampusURL
	}
	if _, ok := hotjobCampusProjects[site.Adapter]; ok {
		return u.String() == site.URL
	}
	if site.Adapter == "bankcomm_tech" {
		return u.String() == BankcommCampusURL
	}
	if site.Adapter == "cmb_tech" || site.Adapter == "citic_tech" || site.Adapter == "boc_software" || site.Adapter == "boc_operations" {
		return bankEntry(site, u)
	}
	if site.Adapter == "ctyun" || site.Adapter == "ctcloud" || site.Adapter == "gbits" {
		return u.String() == site.URL
	}
	if u.RawFragment != "" {
		return false
	}
	if site.Adapter == "leihuo" {
		return u.Host == "leihuo.163.com" && strings.TrimRight(u.Path, "/") == "/campus" && u.RawQuery == "" && u.Fragment == "/full"
	}
	if u.Fragment != "" {
		return false
	}
	switch site.Adapter {
	case "ths":
		return u.String() == THSCampusURL
	case "cmbnt":
		return u.String() == CMBNTCampusURL
	case "netease_game":
		return u.String() == NeteaseGameCampusURL
	case "tcl_digital", "tcl_honghu", "cec_software", "mihoyo", "pingan_tech", "pingan_oneconnect", "pingan_wallet", "cmcloud", "cmiot", "cmhome":
		return u.String() == site.URL
	}
	return false
}

func (a PublicPlatform) discoverSector(ctx context.Context, s d.Source) ([]PostingRef, error) {
	switch s.Adapter {
	case "kedacom":
		return a.discoverKedacom(ctx, s)
	case "games37":
		return a.discoverGames37(ctx, s)
	case "sangfor":
		return a.discoverSangfor(ctx, s)
	case "bankcomm_tech":
		return a.discoverBankcomm(ctx, s)
	case "cms_securities", "htsc_securities", "yonyou":
		return a.discoverHotjob(ctx, s)
	case "cmb_tech":
		return a.discoverCMB(ctx, s)
	case "citic_tech":
		return a.discoverCITIC(ctx, s)
	case "boc_software", "boc_operations":
		return a.discoverBOC(ctx, s)
	case "tcl_digital", "tcl_honghu":
		return a.discoverTCL(ctx, s)
	case "cec_software":
		return a.discoverCEC(ctx, s)
	case "ths":
		return a.discoverTHS(ctx, s)
	case "cmbnt":
		return a.discoverCMBNT(ctx, s)
	case "netease_game":
		return a.discoverNeteaseGame(ctx, s)
	case "leihuo":
		return a.discoverLeihuo(ctx, s)
	case "ctyun", "ctcloud":
		return a.discoverCTCloud(ctx, s)
	case "mihoyo":
		return a.discoverMihoyo(ctx, s)
	case "gbits":
		return a.discoverGBits(ctx, s)
	case "pingan_tech", "pingan_oneconnect", "pingan_wallet":
		return a.discoverPingan(ctx, s)
	case "cmcloud", "cmiot", "cmhome":
		return a.discoverMobile(ctx, s)
	}
	return nil, fail("UNSUPPORTED", false, 0)
}
func (a PublicPlatform) fetchSector(ctx context.Context, s d.Source, r PostingRef) (string, error) {
	switch s.Adapter {
	case "kedacom":
		return a.fetchKedacom(ctx, s, r)
	case "games37":
		return a.fetchGames37(ctx, s, r)
	case "sangfor":
		return a.fetchSangfor(ctx, s, r)
	case "bankcomm_tech":
		return a.fetchBankcomm(ctx, s, r)
	case "cms_securities", "htsc_securities", "yonyou":
		return a.fetchHotjob(ctx, s, r)
	case "cmb_tech":
		return a.fetchCMB(ctx, s, r)
	case "citic_tech":
		return a.fetchCITIC(ctx, s, r)
	case "boc_software", "boc_operations":
		return a.fetchBOC(ctx, s, r)
	case "tcl_digital", "tcl_honghu":
		return a.fetchTCL(ctx, s, r)
	case "cec_software":
		return a.fetchCEC(ctx, s, r)
	case "ths":
		return a.fetchTHS(ctx, s, r)
	case "cmbnt":
		return a.fetchCMBNT(ctx, s, r)
	case "netease_game":
		return a.fetchNeteaseGame(ctx, s, r)
	case "leihuo":
		return a.fetchLeihuo(ctx, s, r)
	case "ctyun", "ctcloud":
		return a.fetchCTCloud(ctx, s, r)
	case "mihoyo":
		return a.fetchMihoyo(ctx, s, r)
	case "gbits":
		return a.fetchGBits(ctx, s, r)
	case "pingan_tech", "pingan_oneconnect", "pingan_wallet":
		return a.fetchPingan(ctx, s, r)
	case "cmcloud", "cmiot", "cmhome":
		return a.fetchMobile(ctx, s, r)
	}
	return "", fail("UNSUPPORTED", false, 0)
}
