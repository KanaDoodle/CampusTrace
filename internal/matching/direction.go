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
	directionPattern("基础架构与平台", `基础架构|基础设施|中间件|云原生|开发平台|研发平台|\binfrastructure\b|\bplatform engineer`, `(?:基础架构|中间件|开发平台|研发平台)(?:设计|开发|研发|建设)|(?:建设|开发|维护)(?:基础架构|中间件|开发平台|研发平台)`),
	directionPattern("前端开发", `前端|\bfront[ -]?end\b`, `前端(?:开发|研发)|(?:开发|实现|维护)(?:网页|web页面|Web 页面)|网页(?:开发|设计)|\bfront[ -]?end (?:development|applications)`),
	directionPattern("移动端开发", `客户端|移动端|\bandroid\b|\bios\b|\bmobile developer`, `(?:客户端|移动端|Android|iOS)(?:开发|研发)|(?:开发|维护)(?:Android|iOS)(?:应用|客户端)`),
	directionPattern("全栈开发", `全栈|\bfull[ -]?stack\b`, `全栈(?:开发|研发)|前后端(?:开发|研发)`),
	directionPattern("数据开发", `数据开发|数据工程|大数据开发|\bdata engineer`, `(?:数据仓库|数据管道|数据平台|数据工程)(?:开发|建设|设计)|(?:建设|开发)(?:数据仓库|数据管道|数据平台)`),
	directionPattern("算法与模型", `算法|机器学习|模型训练|\balgorithm\b|machine learning|\bdata scientist`, `(?:算法|机器学习|模型训练)(?:研究|研发|开发|优化)|(?:研发|研究|训练)(?:算法|机器学习模型|大模型)`),
	directionPattern("安全研发", `安全研发|安全开发|网络安全|信息安全|AI安全|\bsecurity engineer`, `(?:安全平台|安全系统)(?:开发|建设)|(?:开发|建设)(?:安全平台|安全系统)`),
	directionPattern("测试研发", `测试开发|测试研发|软件测试|测试工程师|质量保障|\btest engineer|\bqa engineer`, `(?:自动化测试|软件测试|测试平台)(?:开发|建设)|(?:开发|建设)(?:测试平台|自动化测试框架)`),
	directionPattern("运维与可靠性", `运维|可靠性工程师|\bsre\b|\bdevops\b`, `(?:系统|服务|服务器)(?:运维|部署)|(?:维护|保障)(?:服务|系统)(?:稳定性|可用性)`),
	directionPattern("嵌入式开发", `嵌入式|固件|\bembedded\b|\bfirmware`, `(?:嵌入式|固件)(?:开发|设计)|(?:开发|设计)(?:嵌入式系统|固件)`),
	directionPattern("硬件与电子", `硬件|电子工程师|电路|射频|芯片|\bhardware\b|\belectronics engineer`, `(?:电路|硬件|芯片)(?:设计|开发|验证)|(?:设计|验证)(?:电路|硬件|芯片)`),
	directionPattern("机械与结构", `机械|结构工程师|\bmechanical\b`, `(?:机械|结构)(?:设计|制造)|(?:设计|制造)(?:机械设备|机械结构)`),
	directionPattern("电气与自动化", `电气|自动化工程师|\belectrical\b`, `(?:电气|自动化控制)(?:设计|调试)|(?:设计|调试)(?:电气系统|自动化控制系统)`),
	directionPattern("生产与工艺", `生产工程师|工艺|制造工程师|质量工程师|\bmanufacturing engineer|\bprocess engineer`, `(?:生产工艺|制造工艺)(?:设计|优化)|(?:优化|设计)(?:生产工艺|制造工艺)|生产质量管理`),
	directionPattern("材料与化学", `材料工程师|材料研发|化学|化工|\bchemical\b|\bmaterials engineer`, `(?:材料|化学|化工)(?:研究|研发)|(?:研发|研究)(?:化工材料|新材料)`),
	directionPattern("产品管理", `产品经理|产品运营经理|\bproduct manager`, `产品规划|产品需求分析|制定产品路线`),
	directionPattern("设计", `视觉设计|交互设计|平面设计|工业设计|\bux designer|\bui designer|\bgraphic designer`, `视觉设计|交互设计|平面设计|工业设计`),
	directionPattern("运营", `运营|\boperations specialist`, `内容运营|用户运营|活动运营|电商运营`),
	directionPattern("市场与销售", `市场营销|市场专员|营销|销售|商务拓展|\bmarketing\b|\bsales\b|business development`, `市场推广|销售拓展|客户销售|营销策划|商务拓展`),
	directionPattern("财务与审计", `财务|会计|审计|税务|\baccountant\b|\bauditor\b|\bfinance analyst`, `财务核算|会计核算|财务分析|审计工作|税务申报`),
	directionPattern("人力资源", `人力资源|招聘专员|招聘经理|\bhuman resources\b|\bhr specialist`, `人才招聘|员工招聘|薪酬管理|人力资源管理`),
	directionPattern("法务", `法务|法律顾问|\blegal counsel`, `合同审查|法律咨询|法律事务`),
	directionPattern("供应链与采购", `供应链|采购|物流|\bsupply chain\b|\bprocurement\b`, `供应链管理|采购管理|物流管理|供应商管理`),
}

var directionLinks = [][2]string{
	{"后端开发", "基础架构与平台"}, {"后端开发", "全栈开发"}, {"后端开发", "数据开发"},
	{"基础架构与平台", "运维与可靠性"}, {"基础架构与平台", "数据开发"},
	{"前端开发", "全栈开发"}, {"前端开发", "移动端开发"},
	{"硬件与电子", "嵌入式开发"}, {"硬件与电子", "电气与自动化"},
	{"机械与结构", "生产与工艺"}, {"材料与化学", "生产与工艺"},
}

func directionRoles(text string, duty bool) []string {
	roles := []string{}
	for _, f := range directionFamilies {
		pattern := f.title
		if duty {
			pattern = f.duty
		}
		if pattern.MatchString(text) {
			roles = append(roles, f.name)
		}
	}
	return roles
}

func relatedDirection(a, b string) bool {
	for _, pair := range directionLinks {
		if a == pair[0] && b == pair[1] || a == pair[1] && b == pair[0] {
			return true
		}
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
	add(directionRoles(j.Title, false), "TITLE", j.Title)
	interfaces, storage := false, false
	backendExcerpt := ""
	for _, clause := range parsed.Body {
		add(directionRoles(clause, true), "BODY", clause)
		if backendInterface.MatchString(clause) {
			interfaces, backendExcerpt = true, clause
		}
		storage = storage || backendStorage.MatchString(clause)
	}
	if interfaces && storage {
		add([]string{"后端开发"}, "BODY", backendExcerpt)
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
		direct, related, other = direct || match, related || near, other || (!match && !near)
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
	case parsed.Incomplete:
		v.Reason = "岗位文字未完整识别，暂不判为明显无关。"
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
