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
		"sap": "外企与软件", "siemens": "外企与工业", "haier": "制造业", "sany": "制造业", "sgm": "汽车与制造", "inovance": "制造业", "midea": "制造业", "byd": "汽车与制造", "hikvision": "智能物联与制造",
		"mthreads": "芯片与制造", "nexchip": "芯片与制造",
		"h3c": "通信与制造", "yusys": "金融科技", "cksic": "国央企", "whxmc": "芯片与制造",
		"neusoft": "互联网", "kedacom": "智能物联与制造", "games37": "游戏",
		"yonyou": "互联网", "sangfor": "互联网",
		"bankcomm_tech": "银行", "cms_securities": "证券", "htsc_securities": "证券", "csc_securities": "证券", "guosen_securities": "证券", "galaxy_securities": "证券", "cicc_securities": "证券",
		"cmb_tech": "银行", "citic_tech": "银行", "boc_software": "银行", "boc_operations": "银行",
		"ths": "金融科技", "cmbnt": "金融科技", "netease_game": "游戏", "leihuo": "游戏", "ctyun": "国央企", "ctcloud": "国央企", "mihoyo": "游戏", "pingan_tech": "金融科技", "pingan_oneconnect": "金融科技", "pingan_wallet": "金融科技", "cmcloud": "国央企", "cmiot": "国央企", "cmhome": "国央企", "hundsun": "金融科技", "yuewen": "互联网", "gbits": "游戏", "tcl_digital": "消费电子与制造", "tcl_honghu": "消费电子与制造", "cec_software": "国央企",
	}
	out := make([]CampusDirectoryEntry, 0, 108)
	for _, site := range CampusSites() {
		entry := CampusDirectoryEntry{CampusSite: site, Category: categories[site.Adapter], AutoImport: true, Status: "已接入", Note: "预览时实时核验官网范围与岗位数量。", CheckedAt: "2026-10-05"}
		if _, ok := sectorScopes[site.Adapter]; ok {
			entry.CheckedAt = "2026-10-06"
		}
		switch site.Adapter {
		case "bankcomm_tech":
			entry.CheckedAt = "2026-10-08"
			entry.Note = "完整读取总行校招后选择金融科技项目；不混入业务类、博士后及其他单位。官网入口需筛选总行；招聘季年份不等于毕业年份，具体毕业范围见原文。"
		case "cms_securities", "htsc_securities":
			entry.CheckedAt = "2026-10-08"
			entry.Note = "限定本预设的官网固定校招项目，保留岗位职责、学历、毕业范围、实习考察和截止时间；不据匿名可投状态判断资格。"
		case "csc_securities", "guosen_securities", "galaxy_securities", "cicc_securities":
			entry.CheckedAt = "2026-10-08"
			entry.Note = "完整读取官网校招分类；可能包含实习考察、不同届别、公开地区及经验要求。官网未明确用工类型时保留未知，具体条件见原文。"
		case "cmb_tech", "citic_tech":
			entry.CheckedAt = "2026-10-08"
			entry.Note = "限定科技相关校招岗位，保留原始招聘单位、轮岗培养及届别要求；完整分页读取后筛选，数量以实时预览为准。"
		case "boc_software", "boc_operations":
			entry.CheckedAt = "2026-10-08"
			entry.Note = "限定 2027 校招及本预设单位；官网链接打开集团目录，需按单位查看。部分资格要求引用公告附件，保留核对链接，不自动补写；官网截止时间保留北京时间。"
		case "sap":
			entry.CheckedAt = "2026-10-06"
			entry.Status, entry.Note = "核验时暂无岗位", "中国 Graduate 分类核验为 0 岗，可关注后等待更新；不混入 Student 实习及 Professional 社招。Graduate 不等于统一 2027 应届项目，具体届别与经验要求见原文。"
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
		case "h3c":
			entry.CheckedAt = "2026-10-10"
			entry.Note = "完整读取官网校招分类，含锐进等专项和部分海外地点；保留原始职责、要求、城市、学历及工作性质。具体届别以原文为准。"
		case "yusys":
			entry.CheckedAt = "2026-10-10"
			entry.Note = "完整读取官网校招分类，含高潜人才项目；保留大模型应用、软件研发等岗位的完整原文及公开城市、学历、工作性质。届别以原文为准。"
		case "cksic", "whxmc":
			entry.CheckedAt = "2026-10-10"
			entry.Note = "完整读取官网校招分类，保留软件、AI 与芯片制造等方向的职责、要求、城市、学历及工作性质；具体届别以原文为准。"
		case "neusoft":
			entry.CheckedAt = "2026-10-10"
			entry.Note = "完整读取官网校招分类，保留软件研发等方向的职责与要求；地点为空时保留未知，具体毕业年份见岗位原文。"
		case "kedacom":
			entry.CheckedAt = "2026-10-10"
			entry.Note = "完整读取校园招聘类别 r=2，排除页面混列的实习；保留完整城市、职责、要求及公开截止日期。包含不同届别，以岗位原文为准。"
		case "games37":
			entry.CheckedAt = "2026-10-10"
			entry.Note = "按官网校园招聘查询完整分页读取公开职责与要求，保留官网提供的 Moka 投递链接；不读取候选人数据。具体毕业年份和用工形式以原文为准。"
		case "mthreads", "nexchip":
			entry.CheckedAt = "2026-10-09"
			entry.Note = "按官网校招分类完整分页读取，保留职责与要求；不混入社招、实习。地点字段为空时保留未知，具体毕业年份见岗位原文。"
		case "yonyou":
			entry.CheckedAt = "2026-10-09"
			entry.Note = "仅 2027 届校招（北京）项目；北京为项目名，实际工作城市以岗位为准。保留职责、学历及官网截止时间，不将当前预设视为用友集团或子公司的全部招聘。"
		case "sangfor":
			entry.CheckedAt = "2026-10-09"
			entry.Note = "限定官网 27 届校园招聘常规频道，实时核对频道名称；不混入 X-STAR 专项、实习与社招。保留完整原文、工作城市及全职类型。"
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
		if _, ok := beisenCompanies[site.Adapter]; ok {
			entry.CheckedAt = "2026-10-10"
		}
		out = append(out, entry)
	}
	manual := []struct{ company, category, url, status, note string }{
		{"紫光展锐", "芯片与制造", "https://www.unisoc.com/cn/about/join-us", "公开查询受限", "官网招聘页可读，但官网链接的校招入口与公开查询本次均返回 405，尚未完成完整岗位与详情核验。"},
		{"字节跳动", "互联网", "https://jobs.bytedance.com/campus/position", "公开查询受限", "官网可读；按公开客户端发送匿名岗位查询返回 405，尚未完成校招范围、完整分页与详情核验。"},
		{"携程/Trip.com", "互联网", CtripCampusURL, "官网验证阻断", "公开接口曾返回 56 个应届生岗位；正式安全抓取器收到官网验证跳转，当前不可自动导入。"},
		{"滴滴", "互联网", "https://talent.didiglobal.com/campus/", "访问受限", "官网链接的 Moka 校招入口在本机出现重定向循环。"},
		{"华为", "通信与制造", "https://career.huawei.com/cn/campus-recruitment", "访问受限", "官网校招页可读；公开应届岗位查询本次返回 412，尚未完成正式抓取验证。"},
		{"小米", "消费电子与制造", "https://hr.xiaomi.com/website/campus.html", "投递入口失效", "官网校招分类本次可读 1059 岗，但官网发布的校招、零售项目入口及抽查岗位链接均返回 404；容量扩展与原文核对也尚未完成，暂不自动导入。"},
		{"博世", "外企与工业", "https://www.bosch.com.cn/careers/", "访问受限", "中国官网可读，官网链接的 Moka 校招入口在本机出现重定向循环。"},
		{"ABB", "外企与工业", "https://careers.abb/china/zh/", "待适配", "中国职业页可读；中国毕业生岗位范围与公开查询尚未验证。"},
		{"施耐德电气", "外企与工业", "https://www.se.com/cn/zh/about-us/careers/students-and-young-professionals.jsp", "访问受限", "本机中国页面连接超时，全球职位入口返回 403，尚未完成岗位读取验证。"},
		{"Intel", "外企与芯片", "https://jobs.intel.com/en/locations-china", "访问受限", "中国职位入口在本机返回 403，尚未完成毕业生范围与分页验证。"},
		{"IBM", "外企与软件", "https://www.ibm.com/careers/search", "待适配", "官网搜索页可读；中国毕业生岗位范围尚未验证，不能混入全球社招。"},
		{"爱立信", "外企与通信", "https://jobs.ericsson.com/careers", "公开查询受限", "官网搜索页可读且能看到中国 New Grad 岗位；公开 search 查询本次返回 403，尚未完成完整分页与详情验证。"},
		{"宁德时代", "新能源与制造", "https://talent.catl.com/", "访问受限", "官网 Moka 招聘入口在本机出现重定向循环；2027 校招项目尚未核验。"},
		{"吉利汽车/控股", "汽车与制造", "https://www.geelyautogroup.com/recruitment", "访问受限", "集团招聘页可读；官网链接的校招站点连接失败，需分别核对子公司范围。"},
		{"上汽集团", "汽车与制造", "https://www.saicmotor.com/chinese/rlzy/rcxq/xyzp/index_xz2.shtml", "需按子公司接入", "官网汇总多个子公司渠道；尚未核验各渠道校招范围，不能视为集团统一列表。"},
		{"格力电器", "制造业", "https://www.gree.com/", "招聘入口待确认", "公司官网可读；当前正式校招列表尚未确认，此处保留公司官网入口。"},
	}
	for _, s := range manual {
		checked := "2026-10-05"
		if s.company == "紫光展锐" {
			checked = "2026-10-10"
		}
		switch s.company {
		case "字节跳动", "携程/Trip.com", "滴滴", "华为", "小米", "爱立信":
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

	banks := []struct{ company, url, status, note string }{
		{"工商银行", "https://job.icbc.com.cn/", "公开查询返回异常", "招聘官网可读；匿名校招岗位查询返回系统繁忙，尚未完成完整列表及独立详情核验。"},
		{"农业银行", "https://career.abchina.com.cn/", "客户端协议待适配", "招聘官网可读；公开客户端使用加密请求封装，当前未接通岗位列表和详情，不能视为已支持自动导入。"},
		{"建设银行", "https://job2.ccb.com/cn/job/index.html", "岗位接口待适配", "2027 校招公告可读；按官网协议复核多个查询入口仍返回空正文，尚不能核验岗位列表及详情；建信金科独立保留公告入口。"},
		{"交通银行", "https://job.bankcomm.com/", "需按单位接入", "已接通总行金融科技校招；集团校招总列表超过 500 岗容量，其他招聘单位需分别核验后接入。"},
		{"邮储银行", "https://www.psbc.com/cn/gyyc/rczp/xyzp/", "公告入口", "2027 校招公告及专属网申站可读；网申站使用智联动态组件，公开单位列表与独立岗位详情协议尚未接通。"},
		{"兴业银行", "https://job.cib.com.cn/portal/", "客户端协议待适配", "2027 校招公告已确认科技研发中心研发岗；岗位查询返回错误页面，公开客户端请求封装仍待核验，暂不能自动读取完整岗位。"},
		{"浦发银行", "https://job.spdb.com.cn/", "岗位渠道待核验", "招聘官网可读；本次未读到可核对的公开校招岗位列表，后续需确认当前投递渠道及详情协议。"},
	}
	for _, b := range banks {
		out = append(out, CampusDirectoryEntry{CampusSite: CampusSite{Company: b.company, URL: b.url, Scope: "仅官网入口，暂不支持自动导入"}, Category: "银行", Status: b.status, Note: b.note, CheckedAt: "2026-10-08"})
	}
	securities := []struct{ company, url, status, note string }{
		{"中信证券", "https://careers.citics.com/", "公开协议待适配", "官网可读；公开客户端使用独立招聘网关，总部与分支项目、完整分页及详情尚未完成适配。"},
		{"广发证券", "https://job.gf.com.cn/", "当前校招项目待核验", "官网及其发布的招聘平台可读；校园分类本次混有日常招聘，尚未核验独立当届项目，不能统一视为 2027 校招。"},
		{"国泰海通证券", "https://hr.gtht.com/", "岗位协议待适配", "新版招聘官网可读；校招项目、完整岗位列表及独立详情协议仍待适配，不沿用旧海通或国泰君安渠道冒充当前范围。"},
	}
	for _, s := range securities {
		out = append(out, CampusDirectoryEntry{CampusSite: CampusSite{Company: s.company, URL: s.url, Scope: "仅官网入口，暂不支持自动导入"}, Category: "证券", Status: s.status, Note: s.note, CheckedAt: "2026-10-08"})
	}
	return out
}
