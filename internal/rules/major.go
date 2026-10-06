package rules

import (
	"regexp"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

var computerMajorRequirement = regexp.MustCompile(`计算机(?:类|相关)?(?:专业|及相关专业)|计算机相关`)

// This is a deliberately small family for explicitly broad wording. An exact
// "计算机科学与技术专业" condition remains exact, and degree scope is applied
// by MajorsForRequirement before this function is called.
func MajorMatches(value string, majors []string, excerpt string) bool {
	if alternatives(value, majors) {
		return true
	}
	if !computerMajorRequirement.MatchString(excerpt) {
		return false
	}
	broad := false
	for _, v := range strings.Split(value, "|") {
		broad = broad || v == "计算机" || v == "计算机类" || v == "计算机相关专业"
	}
	if !broad {
		return false
	}
	for _, major := range majors {
		switch d.Normalize(major) {
		case "计算机", "计算机科学与技术", "软件工程", "计算机技术", "计算机应用技术", "计算机软件与理论", "计算机系统结构":
			return true
		}
	}
	return false
}
