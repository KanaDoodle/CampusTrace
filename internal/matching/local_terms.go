package matching

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

type localTerm struct {
	ID, Name, Kind string
	Aliases        []string
}

// These are explicit vocabulary correspondences, not inferred proficiency.
// Implementation details such as goroutine or sync.Mutex remain fact text.
var localTerms = []localTerm{
	{"data_structures", "数据结构", "FOUNDATION", []string{"数据结构", "data structures"}},
	{"algorithms", "算法", "FOUNDATION", []string{"算法", "algorithms"}},
	{"operating_systems", "操作系统", "FOUNDATION", []string{"操作系统", "operating systems", "operating system"}},
	{"computer_networks", "计算机网络", "FOUNDATION", []string{"计算机网络", "computer networks", "computer networking"}},
	{"database_fundamentals", "数据库基础", "FOUNDATION", []string{"数据库基础", "数据库原理", "database fundamentals"}},
	{"computer_architecture", "计算机系统结构", "FOUNDATION", []string{"计算机系统结构", "计算机组成原理", "computer architecture"}},
	{"go", "Go", "LANGUAGE", []string{"go", "golang"}},
	{"java", "Java", "LANGUAGE", []string{"java"}},
	{"cpp", "C++", "LANGUAGE", []string{"c++", "cpp"}},
	{"c", "C", "LANGUAGE", []string{"c"}},
	{"csharp", "C#", "LANGUAGE", []string{"c#", "csharp"}},
	{"python", "Python", "LANGUAGE", []string{"python"}},
	{"javascript", "JavaScript", "LANGUAGE", []string{"javascript", "js"}},
	{"typescript", "TypeScript", "LANGUAGE", []string{"typescript", "ts"}},
	{"rust", "Rust", "LANGUAGE", []string{"rust"}},
	{"php", "PHP", "LANGUAGE", []string{"php"}},
	{"scala", "Scala", "LANGUAGE", []string{"scala"}},
	{"kotlin", "Kotlin", "LANGUAGE", []string{"kotlin"}},
	{"swift", "Swift", "LANGUAGE", []string{"swift"}},
	{"ruby", "Ruby", "LANGUAGE", []string{"ruby"}},
	{"mysql", "MySQL", "TECH", []string{"mysql"}},
	{"postgresql", "PostgreSQL", "TECH", []string{"postgresql", "postgres"}},
	{"redis", "Redis", "TECH", []string{"redis"}},
	{"mongodb", "MongoDB", "TECH", []string{"mongodb", "mongo db"}},
	{"sql", "SQL", "TECH", []string{"sql"}},
	{"linux", "Linux", "TECH", []string{"linux"}},
	{"docker", "Docker", "TECH", []string{"docker"}},
	{"kubernetes", "Kubernetes", "TECH", []string{"kubernetes", "k8s"}},
	{"git", "Git", "TECH", []string{"git"}},
	{"grpc", "gRPC", "TECH", []string{"grpc"}},
	{"spring", "Spring", "TECH", []string{"spring"}},
	{"springboot", "Spring Boot", "TECH", []string{"spring boot", "springboot"}},
	{"gin", "Gin", "TECH", []string{"gin"}},
	{"nodejs", "Node.js", "TECH", []string{"node.js", "nodejs"}},
	{"react", "React", "TECH", []string{"react", "react.js", "reactjs"}},
	{"vue", "Vue", "TECH", []string{"vue", "vue.js", "vuejs"}},
	{"kafka", "Kafka", "TECH", []string{"kafka"}},
	{"rabbitmq", "RabbitMQ", "TECH", []string{"rabbitmq"}},
	{"rocketmq", "RocketMQ", "TECH", []string{"rocketmq"}},
	{"elasticsearch", "Elasticsearch", "TECH", []string{"elasticsearch", "elastic search"}},
	{"task_queue", "异步任务处理", "CAPABILITY", []string{"任务队列", "异步任务", "task queue", "asynchronous tasks", "async tasks"}},
	{"message_queue", "消息队列", "CAPABILITY", []string{"消息队列", "message queue"}},
	{"idempotency", "幂等处理", "CAPABILITY", []string{"幂等", "idempotency", "idempotent"}},
	{"retry", "失败重试", "CAPABILITY", []string{"失败重试", "退避重试", "重试", "retry", "retries"}},
	{"dedup", "去重处理", "CAPABILITY", []string{"去重", "deduplication", "dedup"}},
	{"backpressure", "背压控制", "CAPABILITY", []string{"背压", "backpressure"}},
	{"worker_pool", "工作池与并发控制", "CAPABILITY", []string{"协程池", "线程池", "工作池", "并发控制", "worker pool", "bounded workers"}},
	{"transaction", "数据库事务", "CAPABILITY", []string{"数据库事务", "事务处理", "database transaction", "transactions"}},
}

var termByID = map[string]localTerm{}
var termByAlias = map[string]localTerm{}
var termPattern = func() *regexp.Regexp {
	aliases := []string{}
	for _, term := range localTerms {
		termByID[term.ID] = term
		for _, alias := range term.Aliases {
			termByAlias[strings.ToLower(alias)] = term
			aliases = append(aliases, alias)
		}
	}
	sort.Slice(aliases, func(i, j int) bool { return len(aliases[i]) > len(aliases[j]) })
	for i := range aliases {
		aliases[i] = regexp.QuoteMeta(aliases[i])
	}
	return regexp.MustCompile("(?i)(?:" + strings.Join(aliases, "|") + ")")
}()

func asciiWord(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}
func localFeatures(text string) []string {
	seen := map[string]bool{}
	ids := []string{}
	for _, pos := range termPattern.FindAllStringIndex(text, -1) {
		alias := strings.ToLower(text[pos[0]:pos[1]])
		term := termByAlias[alias]
		if alias[0] < 128 {
			if pos[0] > 0 && asciiWord(text[pos[0]-1]) || pos[1] < len(text) && asciiWord(text[pos[1]]) {
				continue
			}
			// C++/C# do not also prove C; Node.js does not also prove JavaScript.
			if term.ID == "c" && pos[1] < len(text) && strings.ContainsRune("+#", rune(text[pos[1]])) || (alias == "js" || alias == "ts") && pos[0] > 0 && text[pos[0]-1] == '.' {
				continue
			}
		}
		if !seen[term.ID] {
			seen[term.ID] = true
			ids = append(ids, term.ID)
		}
	}
	return ids
}

func shortLocalText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	end := max
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end]
}

var localSentences = regexp.MustCompile(`[\n\r;；。]+`)
var localCommas = regexp.MustCompile(`[,，]`)
var localCue = regexp.MustCompile(`(?i)掌握|熟悉|熟练|了解|精通|具备|具有|擅长|经验|能够|proficien|familiar|knowledge|experience|\bmust\b|\brequired\b`)
var localRequirementLead = regexp.MustCompile(`(?i)^[-*\s0-9.、)（）]*(?:掌握|熟悉|熟练|了解|精通|具备|具有|擅长|proficien|familiar|knowledge|experience|must\b|required\b)`)
var localDuty = regexp.MustCompile(`(?i)负责|参与|开发|设计|实现|维护|建设|优化|搭建|\bbuild\b|\bimplement\b|\bdevelop\b|\bmaintain\b|\bdesign\b`)
var localBonus = regexp.MustCompile(`(?i)优先|加分|优选|更佳|\bpreferred\b|\bbonus\b|nice[ -]to[ -]have`)
var localNegative = regexp.MustCompile(`(?i)未实现|未使用|未掌握|没有|不支持|无需|不要求|不需要|计划|拟实现|待实现|准备实现|\bnot required\b|\bnot implemented\b|\bplanned\b|\bwithout\b`)
var localAlternative = regexp.MustCompile(`(?i)任意.{0,3}(?:种|门|语言)|任一|任一种|其中之?一|至少一|任选|one of|any of|at least one|\bor\b|或者|或`)
var genericLanguage = regexp.MustCompile(`(?i)(?:任意|任一|至少一).{0,8}(?:编程语言|开发语言|programming language)|(?:编程语言|开发语言).{0,8}(?:任意一种|至少一种)|any programming language`)

// Split mixed mandatory/preferred statements, keeping comma-separated lists
// together unless the next fragment introduces a separate requirement.
func localClauses(text string) []string {
	text = strings.TrimSpace(text)
	out := []string{}
	start := 0
	separators := localCommas.FindAllStringIndex(text, -1)
	for i, sep := range separators {
		end := len(text)
		if i+1 < len(separators) {
			end = separators[i+1][0]
		}
		next := text[sep[1]:end]
		if localCue.MatchString(next) || localDuty.MatchString(next) || localBonus.MatchString(next) {
			out = append(out, strings.TrimSpace(text[start:sep[0]]))
			start = sep[1]
		}
	}
	if start < len(text) {
		out = append(out, strings.TrimSpace(text[start:]))
	}
	return out
}
