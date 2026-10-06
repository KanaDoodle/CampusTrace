package matching

import (
	"regexp"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// Direction is independent of skill coverage, city preference and local score.
// Only UNRELATED is suitable for an explicit bulk cleanup; unknown or mixed
// roles are retained for review. It never changes eligibility or deletes jobs.
type Direction struct {
	Status   string              `json:"status"`
	Roles    []string            `json:"roles"`
	Reason   string              `json:"reason"`
	Evidence []DirectionEvidence `json:"evidence"`
}

type DirectionEvidence struct {
	Source  string `json:"source"`
	Excerpt string `json:"excerpt"`
}

type directionFamily struct {
	name        string
	title, duty *regexp.Regexp
}

func directionPattern(name, title, duty string) directionFamily {
	return directionFamily{name, regexp.MustCompile("(?i)" + title), regexp.MustCompile("(?i)" + duty)}
}

// Duty patterns describe work, not a programming language, qualification,
// customer industry or another team mentioned in collaboration requirements.
var directionFamilies = []directionFamily{
	directionPattern("后端开发", `后端|服务端|服务器端|\bback[ -]?end\b|server[ -]side`, `后端(?:开发|研发|服务)|服务端(?:开发|研发)|服务器端(?:开发|研发)|\bback[ -]?end (?:development|services|systems)|server[ -]side (?:development|services)`),
	directionPattern("基础架构与平台", `基础架构|基础设施|中间件|云原生|开发平台|研发平台|云平台|计算平台|大数据(?:计算)?引擎|存储引擎|数据库(?:内核|研发|开发)|分布式(?:存储|计算)|Linux内核|操作系统(?:开发|研发)|编译器|推理(?:系统|引擎|框架)|AI异构计算|高性能计算|资源调度|系统框架|广告引擎|架构(?:研究|研发|开发)|\binfra(?:structure)?\b|\b(?:platform|storage|systems) engineer`, `(?:基础架构|中间件|开发平台|研发平台|云平台|计算平台|存储引擎|数据库内核)(?:设计|开发|研发|建设)|(?:建设|开发|维护)(?:基础架构|中间件|开发平台|研发平台|云平台|计算平台)|(?:建设|开发|研发).{0,12}大数据(?:计算)?引擎`),
	directionPattern("Agent与AI应用开发", `\bCodeAgent\b|\bagent(?:ic)?[ -]*(?:开发|研发|工程|harness|engineer|develop)|\bagent.{0,8}应用|智能体(?:应用)?(?:开发|研发|工程)|AI\s*应用(?:开发|研发|工程)|大模型应用(?:开发|研发|工程)|研究员[-—： ]*大模型应用|\bLLM\s*(?:app|application)\s*(?:engineer|develop)`, `(?:开发|研发|设计|搭建|构建|实现).{0,16}(?:\bAgent\b|智能体|大模型应用|AI\s*应用)|(?:\bAgent\b|智能体|大模型应用|AI\s*应用).{0,12}(?:系统|平台|框架)(?:开发|研发|设计|建设)|\b(?:build|develop|design).{0,20}\b(?:agents|agent systems|LLM applications)\b`),
	directionPattern("前端开发", `前端|\bfront[ -]?end\b`, `前端(?:开发|研发)|(?:开发|实现|维护)(?:网页|web页面|Web 页面)|网页(?:开发|设计)|\bfront[ -]?end (?:development|applications)`),
	directionPattern("移动端开发", `客户端|移动端|\bandroid\b|\bios\b|\bmobile developer`, `(?:客户端|移动端|Android|iOS)(?:开发|研发)|(?:开发|维护)(?:Android|iOS)(?:应用|客户端)`),
	directionPattern("全栈开发", `全栈|\bfull[ -]?stack\b`, `全栈(?:开发|研发)|前后端(?:开发|研发)`),
	directionPattern("数据开发", `数据开发|数据工程|大数据(?:平台)?(?:开发|研发)|\bdata engineer`, `(?:数据仓库|数据管道|数据平台|数据工程)(?:开发|建设|设计)|(?:建设|开发)(?:数据仓库|数据管道|数据平台)`),
	directionPattern("算法与模型", `算法|机器学习|模型训练|后训练|预训练|自然语言处理|计算机视觉|音视频理解|多模态大模型|世界模型|大模型工程师|(?:智能体|大模型)(?:研究|训练)|\balgorithm\b|machine learning|\bdata scientist|\bLLM[ -]*(?:post[ -]?train|research)`, `(?:算法|机器学习|模型训练)(?:研究|研发|开发|优化)|(?:研发|研究|训练)(?:算法|机器学习模型|大模型)`),
	directionPattern("安全研发", `安全研发|安全开发|网络安全|信息安全|AI安全|\bsecurity engineer`, `(?:安全平台|安全系统)(?:开发|建设)|(?:开发|建设)(?:安全平台|安全系统)`),
	directionPattern("测试研发", `测试开发|测试研发|软件测试|测试工程师|质量保障|\btest engineer|\bqa engineer`, `(?:自动化测试|软件测试|测试平台)(?:开发|建设)|(?:开发|建设)(?:测试平台|自动化测试框架)`),
	directionPattern("运维与可靠性", `运维|可靠性工程师|\bsre\b|\bdevops\b`, `(?:系统|服务|服务器)(?:运维|部署)|(?:维护|保障)(?:服务|系统)(?:稳定性|可用性)`),
	directionPattern("嵌入式开发", `嵌入式|固件|\bembedded\b|\bfirmware`, `(?:嵌入式|固件)(?:开发|设计)|(?:开发|设计)(?:嵌入式系统|固件)`),
	directionPattern("硬件与电子", `硬件|电子工程师|电路|射频|芯片|器件|封装|电芯|\bhardware\b|\belectronics engineer`, `(?:电路|硬件|芯片)(?:设计|开发|验证)|(?:设计|验证)(?:电路|硬件|芯片)`),
	directionPattern("机械与结构", `机械|结构工程师|声学|振动|制冷|热管理|\bCAE\b|\bNVH\b|\bmechanical\b`, `(?:机械|结构)(?:设计|制造)|(?:设计|制造)(?:机械设备|机械结构)`),
	directionPattern("电气与自动化", `电气|电机|电控|电源|高压电|弱电|自动化工程师|\belectrical\b`, `(?:电气|电机|电控|自动化控制)(?:设计|调试)|(?:设计|调试)(?:电气系统|电机|自动化控制系统)`),
	directionPattern("生产与工艺", `生产工程师|工艺|制造(?:工程师|管理)|智造技术|产销计划|NPI工程师|质量工程师|\bmanufacturing engineer|\bprocess engineer`, `(?:生产工艺|制造工艺)(?:设计|优化)|(?:优化|设计)(?:生产工艺|制造工艺)|生产质量管理`),
	directionPattern("材料与化学", `材料工程师|材料研发|化学|化工|\bchemical\b|\bmaterials engineer`, `(?:材料|化学|化工)(?:研究|研发)|(?:研发|研究)(?:化工材料|新材料)`),
	directionPattern("产品管理", `产品经理|产品运营经理|\bproduct manager`, `产品规划|产品需求分析|制定产品路线`),
	directionPattern("设计", `视觉设计|交互设计|平面设计|工业设计|创意设计|原画|动画师|灯光师|音效设计|\bux designer|\bui designer|\bgraphic designer`, `视觉设计|交互设计|平面设计|工业设计`),
	directionPattern("运营", `运营|内容策划|游戏(?:关卡|文案)策划|发行管培|\boperations specialist`, `内容运营|用户运营|活动运营|电商运营`),
	directionPattern("市场与销售", `市场营销|市场专员|营销|销售|商务(?:拓展|合作)|渠道管理|售前工程师|\bmarketing\b|\bsales\b|business development`, `市场推广|销售拓展|客户销售|营销策划|商务拓展`),
	directionPattern("财务与审计", `财务|会计|审计|税务|\baccountant\b|\bauditor\b|\bfinance analyst`, `财务核算|会计核算|财务分析|审计工作|税务申报`),
	directionPattern("金融研究与业务", `宏观研究员|行业研究员|投资研究|证券研究|投资经理|交易员|银行柜员|信贷|保险精算`, `宏观研究|行业研究|投资分析|证券研究|信贷审批|保险精算`),
	directionPattern("人力资源", `人力资源|招聘专员|招聘经理|绩效管理|组织专员|\bhuman resources\b|\bhr specialist`, `人才招聘|员工招聘|薪酬管理|人力资源管理`),
	directionPattern("法务", `法务|法律顾问|\blegal counsel`, `合同审查|法律咨询|法律事务`),
	directionPattern("供应链与采购", `供应链|采购|物流|物控|配件管理|\bSCM specialist|\bsupply chain\b|\bprocurement\b`, `供应链管理|采购管理|物流管理|供应商管理`),
	directionPattern("标准与合规", `标准情报|标准法规|认证工程师|合规|知识产权|专利`, `法规符合性|法规合规|标准化文档|知识产权管理|专利申请`),
	directionPattern("游戏图形与技术美术", `游戏(?:引擎|图形|渲染|玩法|脚本)|技术美术|技术策划|\bgraphics engineer`, `(?:游戏引擎|图形渲染|游戏玩法|技术美术)(?:开发|研发|设计)|(?:开发|研发|设计)(?:游戏引擎|图形渲染|游戏玩法)`),
	directionPattern("商业与数据分析", `商业分析|业务分析师|Business Analyst|\bdata analyst`, `商业分析报告|经营分析报告|商业数据分析|经营数据分析|业务指标分析`),
	directionPattern("项目管理", `项目经理|项目主管|项目助理|项目管理|\bproject manager`, `项目进度管理|项目预算管理|项目风险管理`),
	directionPattern("技术支持与交付", `技术支持|技术服务|交付专员|维护工程师|\btechnical support`, `客户技术支持|现场安装调试|客户交付`),
	directionPattern("环境与安全工程", `环境工程师|消防工程师|环保工程师|\benvironmental engineer`, `环保工程|消防工程|环境监测`),
}

var directionLinks = [][2]string{
	{"后端开发", "基础架构与平台"}, {"后端开发", "全栈开发"}, {"后端开发", "数据开发"},
	{"后端开发", "Agent与AI应用开发"}, {"基础架构与平台", "Agent与AI应用开发"}, {"全栈开发", "Agent与AI应用开发"},
	{"后端开发", "安全研发"}, {"Agent与AI应用开发", "安全研发"},
	{"基础架构与平台", "运维与可靠性"}, {"基础架构与平台", "数据开发"},
	{"前端开发", "全栈开发"}, {"前端开发", "移动端开发"},
	{"硬件与电子", "嵌入式开发"}, {"硬件与电子", "电气与自动化"},
	{"机械与结构", "生产与工艺"}, {"材料与化学", "生产与工艺"},
	{"法务", "标准与合规"},
}

const softwareDirection = "软件开发（方向未细分）"

var genericSoftwareTitle = regexp.MustCompile(`(?i)软件(?:开发|研发|工程师)|\bsoftware (?:engineer|developer)|(?:(?:Java|Go|Golang|C\+\+|Python|Rust|C#)\s*)(?:开发|研发)(?:工程师)?`)
var directionCollaboration = regexp.MustCompile(`(?i)^[-*\s0-9.、)（）]*(?:与|和|同|协同|配合|协作|对接|联动|work(?:ing)? with|collaborat(?:e|ing) with)`)
var softwarePlatformContext = regexp.MustCompile(`(?i)软件|代码|程序|编程|数据|计算|云|编译|智能体|\bCI\b|中间件|基础架构`)

func directionRoles(text string, duty bool) []string {
	roles := []string{}
	for _, f := range directionFamilies {
		pattern := f.title
		if duty {
			pattern = f.duty
		}
		if pattern.MatchString(text) {
			if duty && f.name == "基础架构与平台" && (strings.Contains(text, "研发平台") || strings.Contains(text, "开发平台")) && !softwarePlatformContext.MatchString(text) {
				// Chemical/mechanical R&D platforms are not software platforms.
				continue
			}
			roles = append(roles, f.name)
		}
	}
	if !duty && len(roles) == 0 && genericSoftwareTitle.MatchString(text) {
		roles = append(roles, softwareDirection)
	}
	return roles
}

func relatedDirection(a, b string) bool {
	for _, pair := range directionLinks {
		if a == pair[0] && b == pair[1] || a == pair[1] && b == pair[0] {
			return true
		}
	}
	if a == softwareDirection || b == softwareDirection {
		other := b
		if b == softwareDirection {
			other = a
		}
		return hasString([]string{"后端开发", "基础架构与平台", "前端开发", "移动端开发", "全栈开发", "数据开发", "Agent与AI应用开发", "软件开发（方向未细分）"}, other)
	}
	return false
}

func (s *LocalScreener) direction(j d.Job, parsed localParsed) Direction {
	v := Direction{Status: "UNCERTAIN", Roles: []string{}, Evidence: []DirectionEvidence{}}
	if len(s.profile.TargetRoles) == 0 {
		v.Reason = "尚未填写意向职能，请在求职资料中设置主投方向。"
		return v
	}
	add := func(roles []string, source, excerpt string) {
		if len(roles) == 0 {
			return
		}
		for _, role := range roles {
			if !hasString(v.Roles, role) {
				v.Roles = append(v.Roles, role)
			}
		}
		if len(v.Evidence) < 4 {
			v.Evidence = append(v.Evidence, DirectionEvidence{source, shortLocalText(excerpt, 600)})
		}
	}
	titleRoles := directionRoles(j.Title, false)
	if !hasString(titleRoles, softwareDirection) {
		add(titleRoles, "TITLE", j.Title)
	}
	interfaces, storage := false, false
	backendExcerpt := ""
	for _, clause := range parsed.Body {
		if directionCollaboration.MatchString(clause) {
			continue
		}
		add(directionRoles(clause, true), "BODY", clause)
		if backendInterface.MatchString(clause) {
			interfaces, backendExcerpt = true, clause
		}
		storage = storage || backendStorage.MatchString(clause)
	}
	if interfaces && storage {
		add([]string{"后端开发"}, "BODY", backendExcerpt)
	}
	// Generic software titles stay useful for browsing, but specific duties
	// decide the direction when present; a frontend duty is not backend proof.
	if len(v.Roles) == 0 && hasString(titleRoles, softwareDirection) {
		add(titleRoles, "TITLE", j.Title)
	}
	if len(v.Roles) == 0 {
		v.Reason = "标题和职责尚不足以确定方向，保留供核对。"
		return v
	}
	direct, related, other := false, false, false
	for _, role := range v.Roles {
		match, near := false, false
		for _, target := range s.directionTargets {
			match = match || target == role
			near = near || relatedDirection(target, role)
		}
		direct, related, other = direct || match, related || (!match && near), other || (!match && !near)
	}
	switch {
	case (direct || related) && other:
		v.Reason = "岗位涉及多个方向或标题与职责存在冲突，需核对岗位重心。"
	case direct:
		v.Status, v.Reason = "MATCH", "标题或核心职责与主投方向一致。"
		if related {
			v.Status, v.Reason = "RELATED", "岗位包含主投方向及相邻职责，可进一步查看。"
		}
	case related:
		v.Status, v.Reason = "RELATED", "岗位属于与主投方向相邻的职能，建议查看具体职责。"
	case s.directionUnknown:
		v.Reason = "部分意向职能尚未识别，不能据此判定岗位无关。"
	case parsed.DutyIncomplete:
		v.Reason = "岗位职责未完整识别，暂不判为明显无关。"
	default:
		v.Status, v.Reason = "UNRELATED", "已明确识别的岗位职能与主投方向及相邻职能均不同。"
	}
	return v
}

func directionTargets(values []string) ([]string, bool) {
	out, unknown := []string{}, false
	for _, value := range values {
		roles := directionRoles(strings.TrimSpace(value), false)
		unknown = unknown || len(roles) == 0
		for _, role := range roles {
			if !hasString(out, role) {
				out = append(out, role)
			}
		}
	}
	return out, unknown
}
