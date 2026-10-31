package domain

import (
	"regexp"
	"strings"
	"unicode"
)

var goTechnology = regexp.MustCompile(`^(go|golang)(?:\s*1\.\d+(?:\.\d+)?)?$`)
var cppTechnology = regexp.MustCompile(`^c\+\+(?:98|03|11|14|17|20|23|26)?$`)

// TechnologyName normalizes language aliases, not skill levels or proficiency.
func TechnologyName(value string) string {
	v := strings.ToLower(strings.Join(strings.Fields(value), " "))
	if goTechnology.MatchString(v) {
		return "go"
	}
	if cppTechnology.MatchString(v) || v == "cpp" {
		return "c++"
	}
	switch v {
	case "csharp", "c sharp":
		return "c#"
	case "js":
		return "javascript"
	case "ts":
		return "typescript"
	}
	return v
}

// TechnologyExpression returns AND groups containing OR alternatives. In this
// existing wire format '+' separates groups, except the two literal '+' in C++.
// Empty operands and unsupported syntax stay unknown instead of passing/failing
// eligibility accidentally. This is not a general natural-language parser.
func TechnologyExpression(value string) ([][]string, bool) {
	if value == "" || len(value) > 1000 {
		return nil, false
	}
	var groups [][]string
	var group []string
	var token strings.Builder
	push := func() bool {
		name := TechnologyName(token.String())
		token.Reset()
		if name == "" || strings.Contains(name, " or ") || strings.Contains(name, " and ") {
			return false
		}
		if name != "c++" {
			for _, r := range name {
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" _-.#", r) {
					return false
				}
			}
		}
		group = append(group, name)
		return true
	}
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case '+':
			if strings.EqualFold(strings.TrimSpace(token.String()), "c") && i+1 < len(value) && value[i+1] == '+' {
				token.WriteString("++")
				i++
				continue
			}
			if !push() {
				return nil, false
			}
			groups = append(groups, group)
			group = nil
		case '|':
			if !push() {
				return nil, false
			}
		default:
			token.WriteByte(value[i])
		}
	}
	if !push() {
		return nil, false
	}
	return append(groups, group), true
}
