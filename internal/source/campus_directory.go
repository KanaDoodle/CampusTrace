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
		"xiaohongshu": "互联网", "tencent": "互联网", "baidu": "互联网", "meituan": "互联网", "jd": "互联网", "netease": "互联网", "alibaba": "互联网", "bilibili": "互联网", "kuaishou": "互联网", "qihoo360": "互联网",
		"oppo": "消费电子与制造", "vivo": "消费电子与制造", "honor": "消费电子与制造", "lenovo": "消费电子与制造",
		"siemens": "外企与工业", "haier": "制造业", "sany": "制造业", "sgm": "汽车与制造", "inovance": "制造业", "midea": "制造业", "byd": "汽车与制造", "hikvision": "智能物联与制造",
		"ths": "金融科技", "cmbnt": "金融科技", "netease_game": "游戏", "leihuo": "游戏", "ctyun": "国央企", "ctcloud": "国央企", "mihoyo": "游戏", "pingan_tech": "金融科技", "pingan_oneconnect": "金融科技", "pingan_wallet": "金融科技", "cmcloud": "国央企", "cmiot": "国央企", "cmhome": "国央企", "hundsun": "金融科技", "yuewen": "互联网", "gbits": "游戏", "tcl_digital": "消费电子与制造", "tcl_honghu": "消费电子与制造", "cec_software": "国央企",
	}
	out := make([]CampusDirectoryEntry, 0, 75)
	for _, site := range CampusSites() {
		entry := CampusDirectoryEntry{CampusSite: site, Category: categories[site.Adapter], AutoImport: true, Status: "已接入", Note: "预览时实时核验官网范围与岗位数量。", CheckedAt: "2026-10-05"}
		if _, ok := sectorScopes[site.Adapter]; ok {
			entry.CheckedAt = "2026-10-06"
		}
		switch site.Adapter {
		case "tencent":
			entry.CheckedAt = "2026-10-06"
			entry.Note = "限定 2027 常规应届校招及官网中国工作地点分类；每次核对官网项目映射，保留毕业范围、职责、要求与加分项，不混入实习、青云专项和海外 Workday 岗位。"
		case "tcl_digital", "tcl_honghu":
			entry.Note = "限定 2027 全球校招和所选部门，保留职责、要求与毕业范围；不混入 TCL 其他部门。"
		case "cec_software":
			entry.Note = "仅读取麒麟软件、中电云两个单位的校招岗位；官网链接打开集团校招页，可按单位筛选查看，导入范围以本预设为准。具体届别、截止日期见原文。"
		case "hundsun", "yuewen":
			entry.CheckedAt = "2026-10-06"
			entry.Note = "按官网应届生校招分类读取；不混入实习、社招。地点字段为空时保留未知，具体毕业年份见岗位原文。"
		case "gbits":
			entry.Note = "限定 2027 秋招正式岗位，保留职责、要求、加分项与提前实习说明；用工类型保留未知。官网链接打开标题搜索页，同名岗位通过编号分别读取原文。"
		case "meituan":
			entry.Status, entry.Note = "直连受限", "已有适配器；本机正式直连曾超时，能否导入以实时预览为准。"
		case "inovance":
			entry.Status, entry.Note = "核验时暂无岗位", "校招分类本次核验返回 0 岗，可关注后等待更新；不代表其他渠道没有招聘。"
		case "sgm":
			entry.Note = "官网校招分类核验为 1 条秋招项目，包含多类储备生；具体岗位方向以原文为准。"
		case "qihoo360":
			entry.Note = "校招分类包含不同届别，具体毕业年份请核对岗位原文。"
		case "ths":
			entry.Note = "2027 校招项目含实习转正岗位，官网未提供可核对的用工类型；保留原文，不统一标成全职。"
		case "cmbnt":
			entry.Status, entry.Note = "核验时暂无岗位", "应届毕业生分类核验返回 0 岗，可关注后等待更新；不代表社招、实习或其他渠道没有岗位。"
		case "mihoyo":
			entry.Note = "限定 2027 届应届生项目，分别保留职责、要求、加分项和毕业时间范围；不混入实习项目。"
		case "pingan_tech", "pingan_oneconnect", "pingan_wallet":
			entry.Note = "按官网应届生分类与招聘单位读取，不混入实习及集团其他单位；具体毕业届别以岗位原文为准。"
		case "cmcloud", "cmiot", "cmhome":
			entry.Note = "按官网校招分类与招聘单位完整读取公开列表原文；不混入实习、社招，具体届别以岗位原文为准。"
		case "ctyun", "ctcloud":
			entry.Note = "按招聘单位限定 2027 秋招，避免混入集团其他单位；官网链接打开该岗位的标题搜索页。"
		}
		out = append(out, entry)
	}
	manual := []struct{ company, category, url, status, note string }{
		{"字节跳动", "互联网", "https://jobs.bytedance.com/campus/position", "公开查询受限", "官网可读；按公开客户端发送匿名岗位查询返回 405，尚未完成校招范围、完整分页与详情核验。"},
		{"携程/Trip.com", "互联网", CtripCampusURL, "官网验证阻断", "公开接口曾返回 56 个应届生岗位；正式安全抓取器收到官网验证跳转，当前不可自动导入。"},
		{"滴滴", "互联网", "https://talent.didiglobal.com/campus/", "访问受限", "官网链接的 Moka 校招入口在本机出现重定向循环。"},
		{"华为", "通信与制造", "https://career.huawei.com/cn/campus-recruitment", "访问受限", "官网校招页可读；公开应届岗位查询本次返回 412，尚未完成正式抓取验证。"},
		{"小米", "消费电子与制造", "https://hr.xiaomi.com/website/campus.html", "范围待拆分", "官网校招分类本次返回 1061 岗，北京分类也有 529 岗，超过单来源 500 岗上限；需完善可核对的范围划分与详情后接入。"},
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
		checked := "2026-10-05"
		switch s.company {
		case "字节跳动", "携程/Trip.com", "滴滴", "华为", "小米":
			checked = "2026-10-06"
		}
		out = append(out, CampusDirectoryEntry{CampusSite: CampusSite{Company: s.company, URL: s.url, Scope: "仅官网入口，暂不支持自动导入"}, Category: s.category, Status: s.status, Note: s.note, CheckedAt: checked})
	}
	sectors := []struct{ company, category, url, status, note string }{
		{"巨人网络", "游戏", "https://hr.ztgame.com/campus/", "访问受限", "官方校招列表可读；官网链接的 Moka 岗位入口在本机出现重定向循环，尚未接通独立岗位详情。"},
		{"完美世界", "游戏", "https://jobs.games.wanmei.com/", "项目待核验", "官方招聘入口可读；当届正式校招项目、完整岗位范围与第三方详情尚未核验。"},
		{"中国银联", "金融科技", "https://join.unionpay.com/wt/unionpayhr/web/index", "待适配", "官网招聘入口可读；当前校园项目、完整岗位范围与独立详情尚未核验。"},
		{"京东方", "消费电子与制造", "https://campus.boe.com/", "渠道待适配", "官网可读，标准校招查询返回空列表；官网定制招聘渠道尚未完成岗位范围与分页核验，不能据此判断没有招聘。"},
		{"TCL", "消费电子与制造", "https://campus.tcl.com/campus/recruiting.html?jobmx=308501", "需按部门接入", "已支持流程与数字化转型中心、鸿鹄实验室；集团全部岗位每页 10 条，其他部门需分别核验后接入。"},
		{"鹰角网络", "游戏", "https://career.hypergryph.com/", "访问受限", "官网已发布 2027 校招，链接的岗位站本次返回 403；暂不能自动读取完整岗位与详情。"},
		{"叠纸游戏", "游戏", "https://career.papegames.com/campus", "岗位入口待确认", "官网校招页可读，公开客户端链接的岗位列表返回 404；待确认新岗位入口。"},
		{"莉莉丝游戏", "游戏", "https://jobs.lilith.com/", "岗位入口失效", "招聘官网可读，但官网发布的校招链接当前返回 404；暂不能自动导入岗位。"},
		{"建信金科", "金融科技", "https://job2.ccb.com/cn/job/announcement.html?annoId=20260903100124717047&planType=XY", "公告入口", "已确认 2027 校招公告；具体岗位列表、招聘单位与详情待适配。"},
		{"中国平安", "金融科技", "https://campus.pingan.com/", "需按单位接入", "已支持平安科技、金融壹账通和平安壹钱包；集团应届生总列表超过单来源容量，其他单位需独立核验。"},
		{"工银科技", "金融科技", "https://www.tech.icbc.com.cn/", "招聘入口待核验", "保留公司官网及校园招聘栏目；当前届别、独立岗位列表与详情尚未核验。"},
		{"中国移动", "国央企", "https://job.10086.cn/", "需按单位接入", "已支持云公司、物联网公司和智慧家庭运营中心；按单位读取官网校招分类，其他单位需独立核验。"},
		{"中国电信（集团入口）", "国央企", "https://job.chinatelecom.com.cn/", "需按单位接入", "已分别支持天翼云和云计算研究院；集团其他单位需独立核验，集团总列表超过单来源容量。"},
		{"中国联通", "国央企", "https://www.chinaunicom.com.cn/46/menu01/528/column06", "项目待核验", "保留集团官方校招入口；需核对当届公告及各招聘单位的具体岗位渠道。"},
		{"国网信通产业集团", "国央企", "https://campus.51job.com/m/SGIT2027/", "公告入口", "2027 校招页可读，当前公开的是单位介绍和岗位类别；缺少可独立核对的岗位编号及职责详情。"},
		{"南瑞集团", "国央企", "https://job.sgepri.sgcc.com.cn/", "访问受限", "岗位入口本次访问受限；当前校招项目、招聘单位和完整详情尚未核验。"},
		{"中国电科", "国央企", "https://www.cetc.com.cn/zgdk/1593022/1592495/index.html", "公告入口", "已确认集团 2027 校招公告；各成员单位岗位及投递入口需分别核验。"},
		{"中国电子", "国央企", "https://campus.cec.com.cn/", "需按单位接入", "已支持麒麟软件、中电云两单位；集团校招列表超过单来源容量，其他单位需分别核验后接入。"},
	}
	for _, s := range sectors {
		out = append(out, CampusDirectoryEntry{CampusSite: CampusSite{Company: s.company, URL: s.url, Scope: "仅官网入口，暂不支持自动导入"}, Category: s.category, Status: s.status, Note: s.note, CheckedAt: "2026-10-06"})
	}
	return out
}
