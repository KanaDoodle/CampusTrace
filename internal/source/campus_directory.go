package source

// The directory includes official entries that cannot yet be imported. Only
// CampusSites participates in URL recognition and private source registration.
type CampusDirectoryEntry struct {
	CampusSite
	Category   string `json:"category"`
	AutoImport bool   `json:"auto_import"`
	Status     string `json:"status"`
	Note       string `json:"note"`
	CheckedAt  string `json:"checked_at"`
}

func CampusDirectory() []CampusDirectoryEntry {
	categories := map[string]string{
		"xiaohongshu": "互联网", "baidu": "互联网", "meituan": "互联网", "jd": "互联网", "netease": "互联网", "alibaba": "互联网", "bilibili": "互联网", "kuaishou": "互联网", "qihoo360": "互联网",
		"oppo": "消费电子与制造", "vivo": "消费电子与制造", "honor": "消费电子与制造", "lenovo": "消费电子与制造",
		"siemens": "外企与工业", "haier": "制造业", "sany": "制造业", "sgm": "汽车与制造", "inovance": "制造业", "midea": "制造业", "byd": "汽车与制造", "hikvision": "智能物联与制造",
	}
	out := make([]CampusDirectoryEntry, 0, 38)
	for _, site := range CampusSites() {
		entry := CampusDirectoryEntry{CampusSite: site, Category: categories[site.Adapter], AutoImport: true, Status: "已接入", Note: "预览时实时核验官网范围与岗位数量。", CheckedAt: "2026-10-05"}
		switch site.Adapter {
		case "meituan":
			entry.Status, entry.Note = "直连受限", "已有适配器；本机正式直连曾超时，能否导入以实时预览为准。"
		case "inovance":
			entry.Status, entry.Note = "核验时暂无岗位", "校招分类本次核验返回 0 岗，可关注后等待更新；不代表其他渠道没有招聘。"
		case "sgm":
			entry.Note = "官网校招分类核验为 1 条秋招项目，包含多类储备生；具体岗位方向以原文为准。"
		case "qihoo360":
			entry.Note = "校招分类包含不同届别，具体毕业年份请核对岗位原文。"
		}
		out = append(out, entry)
	}
	manual := []struct{ company, category, url, status, note string }{
		{"腾讯", "互联网", "https://join.qq.com/", "访问受限", "官网首页可读，公开客户端资源返回 403；完整校招列表与详情尚未接通。"},
		{"字节跳动", "互联网", "https://jobs.bytedance.com/campus/position", "待适配", "官网与客户端可读；公开查询路由、校招项目范围和完整分页尚未验证。"},
		{"携程/Trip.com", "互联网", CtripCampusURL, "官网验证阻断", "公开接口曾返回 56 个应届生岗位；正式安全抓取器收到官网验证跳转，当前不可自动导入。"},
		{"滴滴", "互联网", "https://talent.didiglobal.com/campus/", "访问受限", "官网链接的 Moka 校招入口在本机出现重定向循环。"},
		{"华为", "通信与制造", "https://career.huawei.com/cn/campus-recruitment", "访问受限", "本机官网连接超时；此前岗位请求返回 403，尚未完成正式抓取验证。"},
		{"小米", "消费电子与制造", "https://hr.xiaomi.com/website/campus.html", "范围待拆分", "此前公开校招查询超过单来源 500 岗上限；需核验官网项目划分后接入。"},
		{"博世", "外企与工业", "https://www.bosch.com.cn/careers/", "访问受限", "中国官网可读，官网链接的 Moka 校招入口在本机出现重定向循环。"},
		{"ABB", "外企与工业", "https://careers.abb/china/zh/", "待适配", "中国职业页可读；中国毕业生岗位范围与公开查询尚未验证。"},
		{"施耐德电气", "外企与工业", "https://www.se.com/cn/zh/about-us/careers/students-and-young-professionals.jsp", "访问受限", "本机中国页面连接超时，全球职位入口返回 403，尚未完成岗位读取验证。"},
		{"SAP", "外企与软件", "https://jobs.sap.com/", "待适配", "官网此次可读；中国地点、毕业生分类及完整分页仍待验证。"},
		{"Intel", "外企与芯片", "https://jobs.intel.com/en/locations-china", "访问受限", "中国职位入口在本机返回 403，尚未完成毕业生范围与分页验证。"},
		{"IBM", "外企与软件", "https://www.ibm.com/careers/search", "待适配", "官网搜索页可读；中国毕业生岗位范围尚未验证，不能混入全球社招。"},
		{"爱立信", "外企与通信", "https://jobs.ericsson.com/careers", "待适配", "官网搜索页可读；中国地点、毕业生范围及完整详情尚未验证。"},
		{"宁德时代", "新能源与制造", "https://talent.catl.com/", "访问受限", "官网 Moka 招聘入口在本机出现重定向循环；2027 校招项目尚未核验。"},
		{"吉利汽车/控股", "汽车与制造", "https://www.geelyautogroup.com/recruitment", "访问受限", "集团招聘页可读；官网链接的校招站点连接失败，需分别核对子公司范围。"},
		{"上汽集团", "汽车与制造", "https://www.saicmotor.com/chinese/rlzy/rcxq/xyzp/index_xz2.shtml", "需按子公司接入", "官网汇总多个子公司渠道；尚未核验各渠道校招范围，不能视为集团统一列表。"},
		{"格力电器", "制造业", "https://www.gree.com/", "招聘入口待确认", "公司官网可读；当前正式校招列表尚未确认，此处保留公司官网入口。"},
	}
	for _, s := range manual {
		out = append(out, CampusDirectoryEntry{CampusSite: CampusSite{Company: s.company, URL: s.url, Scope: "仅官网入口，暂不支持自动导入"}, Category: s.category, Status: s.status, Note: s.note, CheckedAt: "2026-10-05"})
	}
	return out
}
