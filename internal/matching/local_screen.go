package matching

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

type LocalEvidence struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Excerpt string `json:"excerpt"`
}
type LocalCheck struct {
	Category string          `json:"category"`
	Mode     string          `json:"mode"`
	Terms    []string        `json:"terms"`
	Excerpt  string          `json:"excerpt"`
	Result   string          `json:"result"`
	Evidence []LocalEvidence `json:"evidence"`
}
type LocalScreen struct {
	Version        string       `json:"version"`
	Score          float64      `json:"score"`
	Tier           string       `json:"tier"`
	Role           string       `json:"role"`
	RoleSource     string       `json:"role_source"`
	RoleExcerpt    string       `json:"role_excerpt"`
	Direction      Direction    `json:"direction"`
	Reasons        []string     `json:"reasons"`
	Warnings       []string     `json:"warnings"`
	Checks         []LocalCheck `json:"checks"`
	ExcludedReason string       `json:"excluded_reason"`
}
type LocalScreener struct {
	profile          d.Profile
	evidence         map[string][]LocalEvidence
	targetRoles      []string
	unknownTargets   []string
	directionTargets []string
	directionUnknown bool
	preferredCities  map[string]bool
	acceptableCities map[string]bool
}

func NewLocalScreener(p d.Profile, candidate Candidate) *LocalScreener {
	p = p.EducationProfile()
	s := &LocalScreener{profile: p, evidence: map[string][]LocalEvidence{}, preferredCities: map[string]bool{}, acceptableCities: map[string]bool{}}
	s.directionTargets, s.directionUnknown = directionTargets(p.TargetRoles)
	for _, target := range p.TargetRoles {
		families := detectLocalRoles(target)
		s.targetRoles = append(s.targetRoles, families...)
		if len(families) == 0 && len(Normalize(target)) >= 2 {
			s.unknownTargets = append(s.unknownTargets, target)
		}
	}
	for _, city := range p.PreferredCities {
		if norm := Normalize(city); norm != "" {
			s.preferredCities[norm] = true
		}
	}
	for _, city := range p.AcceptableCities {
		if norm := Normalize(city); norm != "" {
			s.acceptableCities[norm] = true
		}
	}
	for _, fact := range candidate.Facts {
		if fact.Kind != "SKILL" && fact.Kind != "LANGUAGE" && fact.Kind != "IMPLEMENTED" {
			continue
		}
		for _, sentence := range localSentences.Split(fact.Text, -1) {
			for _, clause := range localClauses(sentence) {
				if localNegative.MatchString(clause) {
					continue
				}
				clause = shortLocalText(clause, 600)
				for _, id := range localFeatures(clause) {
					s.evidence[id] = append(s.evidence[id], LocalEvidence{fact.ID, fact.Kind, shortLocalText(clause, 600)})
				}
			}
		}
	}
	return s
}

type roleFamily struct {
	name    string
	pattern *regexp.Regexp
}

var roleFamilies = []roleFamily{
	{"后端开发", regexp.MustCompile(`(?i)后端|服务端|服务器端|\bbackend\b|server[ -]side`)},
	{"基础架构与平台", regexp.MustCompile(`(?i)基础架构|基础设施|中间件|云原生|开发平台|研发平台|\binfrastructure\b|\bplatform engineer`)},
	{"前端开发", regexp.MustCompile(`(?i)前端|\bfrontend\b|front[ -]end`)},
	{"移动端开发", regexp.MustCompile(`(?i)客户端|移动端|\bandroid\b|\bios\b`)},
	{"数据开发", regexp.MustCompile(`(?i)数据开发|数据工程|大数据|\bdata engineer`)},
	{"算法与模型", regexp.MustCompile(`(?i)算法|机器学习|模型训练|\balgorithm\b|machine learning`)},
	{"安全研发", regexp.MustCompile(`(?i)安全|\bsecurity\b`)},
	{"测试研发", regexp.MustCompile(`(?i)测试|质量保障|\btesting\b|\bqa\b`)},
}

func detectLocalRoles(text string) []string {
	out := []string{}
	for _, role := range roleFamilies {
		if role.pattern.MatchString(text) {
			out = append(out, role.name)
		}
	}
	return out
}
func hasString(values []string, v string) bool {
	for _, value := range values {
		if value == v {
			return true
		}
	}
	return false
}

var backendInterface = regexp.MustCompile(`(?i)服务接口|后端接口|业务接口|接口服务|api\s*接口|rpc|grpc|restful|http\s*接口|微服务|\bapi\b|\bbackend services\b`)
var backendStorage = regexp.MustCompile(`(?i)数据库|缓存|mysql|postgres|redis|任务队列|异步任务|消息队列|database|message queue|task queue`)

func (s *LocalScreener) localRole(j d.Job, parsed localParsed) (score float64, role, source, excerpt, reason string) {
	titleRoles := detectLocalRoles(j.Title)
	if len(titleRoles) > 0 {
		role, source, excerpt = strings.Join(titleRoles, "、"), "TITLE", j.Title
	}
	targets := s.targetRoles
	for _, family := range titleRoles {
		if hasString(targets, family) {
			return 30, role, source, excerpt, "岗位标题方向与意向职能一致。"
		}
	}
	if hasString(targets, "后端开发") && hasString(titleRoles, "基础架构与平台") || hasString(targets, "基础架构与平台") && hasString(titleRoles, "后端开发") {
		return 16, role, source, excerpt, "岗位方向与意向职能相关，需要进一步核对具体职责。"
	}
	// Only responsibilities can establish a role from a generic title. A
	// language mention or company introduction cannot turn a job into backend.
	interfaces, storage, dutyExcerpt := false, false, ""
	for _, clause := range parsed.Body {
		if !localDuty.MatchString(clause) {
			continue
		}
		bodyRoles := detectLocalRoles(clause)
		for _, family := range bodyRoles {
			if hasString(targets, family) {
				if len(titleRoles) == 0 {
					return 26, family, "BODY", clause, "标题较笼统，岗位职责出现了意向职能的明确线索。"
				}
				return 8, role, source, excerpt, "标题指向其他方向，职责中存在部分意向职能线索，需核对岗位重心。"
			}
		}
		if backendInterface.MatchString(clause) {
			interfaces = true
			dutyExcerpt = clause
		}
		if backendStorage.MatchString(clause) {
			storage = true
			if dutyExcerpt == "" {
				dutyExcerpt = clause
			}
		}
	}
	if hasString(targets, "后端开发") && interfaces && storage {
		if len(titleRoles) == 0 {
			return 26, "后端开发", "BODY", dutyExcerpt, "岗位职责包含服务接口及数据库、缓存或异步任务线索，倾向后端方向。"
		}
		return 8, role, source, excerpt, "职责有后端相关线索，但标题指向其他方向，需要核对岗位重心。"
	}
	for _, target := range s.unknownTargets {
		if strings.Contains(Normalize(j.Title), Normalize(target)) {
			return 30, target, "TITLE", j.Title, "岗位标题包含意向职能。"
		}
	}
	if len(s.profile.TargetRoles) == 0 {
		reason = "尚未填写意向职能，未计方向优先级。"
	} else if role == "" {
		reason = "尚未识别岗位方向，保留供核对。"
	} else {
		reason = "已识别的岗位方向与意向职能不同。"
	}
	return
}

func (s *LocalScreener) Screen(j d.Job, text string) LocalScreen {
	parsed := localJobCache.get(text)
	v := LocalScreen{Version: LocalVersion, Tier: "UNCERTAIN", Reasons: []string{}, Warnings: []string{}, Checks: []LocalCheck{}}
	v.Direction = s.direction(j, parsed)
	roleScore, role, source, excerpt, roleReason := s.localRole(j, parsed)
	v.Role, v.RoleSource, v.RoleExcerpt = role, source, excerpt
	v.Reasons = append(v.Reasons, roleReason)
	prefScore := 0.0
	preferred, acceptable := false, false
	for _, city := range j.Locations {
		norm := Normalize(city)
		preferred = preferred || s.preferredCities[norm]
		acceptable = acceptable || s.acceptableCities[norm]
	}
	switch {
	case preferred:
		prefScore += 12
		v.Reasons = append(v.Reasons, "工作地点包含优先城市。")
	case acceptable:
		prefScore += 8
		v.Reasons = append(v.Reasons, "工作地点包含可接受城市。")
	case len(j.Locations) == 0:
		v.Reasons = append(v.Reasons, "岗位地点未知，未计城市偏好。")
	case len(s.profile.PreferredCities)+len(s.profile.AcceptableCities) == 0:
		v.Reasons = append(v.Reasons, "未设置城市偏好。")
	default:
		v.Reasons = append(v.Reasons, "工作地点未命中城市偏好，仍保留供核对。")
	}
	if j.JobType != "" && hasString(s.profile.PreferredTypes, j.JobType) {
		prefScore += 8
		v.Reasons = append(v.Reasons, "岗位类型符合偏好。")
	}
	weights := map[string]float64{"REQUIRED": 3, "RESPONSIBILITY": 2, "BONUS": 1}
	matched, projectMatched, total := 0.0, 0.0, 0.0
	requiredMissing := 0
	for _, req := range parsed.Requirements {
		check := LocalCheck{Category: req.Category, Mode: req.Mode, Excerpt: req.Excerpt, Terms: []string{}, Evidence: []LocalEvidence{}, Result: "NO_EVIDENCE"}
		hits, projects := 0.0, 0.0
		seen := map[string]bool{}
		for _, id := range req.Terms {
			name := termByID[id].Name
			evidence := s.evidence[id]
			if id == "any_language" {
				name = "任意一种编程语言"
				for _, term := range localTerms {
					if term.Kind == "LANGUAGE" {
						evidence = append(evidence, s.evidence[term.ID]...)
					}
				}
			}
			check.Terms = append(check.Terms, name)
			if len(evidence) == 0 {
				continue
			}
			hits++
			project := false
			for _, e := range evidence {
				if e.Kind == "IMPLEMENTED" {
					project = true
				}
				key := e.ID + "\n" + e.Excerpt
				if !seen[key] {
					check.Evidence = append(check.Evidence, e)
					seen[key] = true
				}
			}
			if project {
				projects++
			}
		}
		// Keep an implemented citation visible when many repeated profile
		// skills would otherwise crowd out the source of the project bonus.
		orderedEvidence := make([]LocalEvidence, 0, len(check.Evidence))
		for _, kind := range []string{"IMPLEMENTED", "SKILL", "LANGUAGE"} {
			seenText := map[string]bool{}
			for _, e := range check.Evidence {
				if e.Kind == kind && !seenText[e.Excerpt] {
					orderedEvidence = append(orderedEvidence, e)
					seenText[e.Excerpt] = true
				}
			}
		}
		if len(orderedEvidence) > 6 {
			orderedEvidence = orderedEvidence[:6]
		}
		check.Evidence = orderedEvidence
		ratio, projectRatio := hits/float64(len(req.Terms)), projects/float64(len(req.Terms))
		if req.Mode == "ANY" {
			if hits > 0 {
				ratio = 1
			}
			if projects > 0 {
				projectRatio = 1
			}
		}
		if ratio == 1 {
			check.Result = "SIGNAL"
		} else if ratio > 0 {
			check.Result = "PARTIAL"
		}
		if req.Category == "REQUIRED" && ratio < 1 {
			requiredMissing++
		}
		weight := weights[req.Category]
		matched += ratio * weight
		projectMatched += projectRatio * weight
		total += weight
		v.Checks = append(v.Checks, check)
	}
	capabilityScore, projectScore := 0.0, 0.0
	if total > 0 {
		capabilityScore = 30 * matched / total
		projectScore = 20 * projectMatched / total
		v.Reasons = append(v.Reasons, "技术线索按必需项、工作内容、加分项分别计权；已确认的实现事实提供额外项目依据。")
	} else {
		v.Warnings = append(v.Warnings, "未提取到词表内的明确技术要求，当前优先级主要依据意向偏好，需要阅读原文或深度分析。")
	}
	if requiredMissing > 0 {
		v.Warnings = append(v.Warnings, "部分必需项在资料中暂无完整依据；这不代表你不具备该能力。")
	}
	if parsed.Incomplete {
		v.Warnings = append(v.Warnings, "部分文字无法可靠归类或超出本地解析范围，初筛可能遗漏要求。")
	}
	v.ExcludedReason = localQualificationGate(s.profile, parsed, &v.Warnings)
	v.Score = math.Round((roleScore+prefScore+capabilityScore+projectScore)*10) / 10
	switch {
	case v.ExcludedReason != "":
		v.Tier = "LOW"
		v.Score = 0
	case total == 0:
		v.Tier = "UNCERTAIN"
	case roleScore >= 16 && v.Score >= 65 && requiredMissing == 0 && !parsed.Incomplete:
		v.Tier = "HIGH"
	case matched > 0 && roleScore > 0:
		v.Tier = "POSSIBLE"
	case role == "" || parsed.Incomplete || requiredMissing > 0:
		v.Tier = "UNCERTAIN"
	default:
		v.Tier = "LOW"
	}
	return v
}

// Reject only unambiguous minimum-degree/year conditions. Conflicting values,
// preferred qualifications and month-level ranges require human verification.
func localQualificationGate(p d.Profile, parsed localParsed, warnings *[]string) string {
	if parsed.Incomplete {
		*warnings = append(*warnings, "本地解析不完整，学历与毕业届别留待进一步核对。")
		return ""
	}
	values := map[string]map[string]bool{}
	for _, q := range parsed.Qualifications {
		if values[q.Kind] == nil {
			values[q.Kind] = map[string]bool{}
		}
		values[q.Kind][q.Value] = true
	}
	degreeRank := map[string]int{"ASSOCIATE": 1, "BACHELOR": 2, "MASTER": 3, "PHD": 4}
	for _, kind := range []string{"DEGREE", "GRADUATION"} {
		if len(values[kind]) != 1 {
			*warnings = append(*warnings, map[string]string{"DEGREE": "学历条件缺失或存在冲突，尚未确认投递资格。", "GRADUATION": "毕业届别条件缺失或存在冲突，尚未确认投递资格。"}[kind])
			continue
		}
		for value := range values[kind] {
			if kind == "DEGREE" {
				if degreeRank[p.Degree] == 0 {
					*warnings = append(*warnings, "求职资料尚未明确学历，需补充核对。")
				} else if degreeRank[p.Degree] < degreeRank[value] {
					return "明确的最低学历条件不符合"
				}
				continue
			}
			allowed := map[int]bool{}
			if strings.Contains(value, "-") {
				bounds := strings.Split(value, "-")
				from, _ := strconv.Atoi(bounds[0])
				to, _ := strconv.Atoi(bounds[1])
				if to-from <= 10 && to >= from {
					for year := from; year <= to; year++ {
						allowed[year] = true
					}
				}
			} else {
				for _, year := range strings.Split(value, "|") {
					y, _ := strconv.Atoi(year)
					allowed[y] = true
				}
			}
			if len(allowed) == 0 {
				*warnings = append(*warnings, "毕业届别范围需核对。")
				continue
			}
			if p.GraduationYear != 0 {
				if !allowed[p.GraduationYear] {
					return "明确的毕业届别条件不符合"
				}
				continue
			}
			if p.GraduationFrom == 0 || p.GraduationTo < p.GraduationFrom || p.GraduationTo-p.GraduationFrom > 10 {
				*warnings = append(*warnings, "求职资料尚未明确毕业届别，需补充核对。")
				continue
			}
			matches, count := 0, p.GraduationTo-p.GraduationFrom+1
			for year := p.GraduationFrom; year <= p.GraduationTo; year++ {
				if allowed[year] {
					matches++
				}
			}
			if matches == 0 {
				return "明确的毕业届别条件不符合"
			}
			if matches < count {
				*warnings = append(*warnings, "个人毕业范围仅部分落在要求内，需确认具体毕业年份。")
			}
		}
	}
	return ""
}
