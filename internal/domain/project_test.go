package domain

import (
	"strings"
	"testing"
)

func TestProjectContentBoundsAndLegacyShape(t *testing.T) {
	for _, p := range []Project{{Name: "旧项目"}, {Name: "完整项目", Description: "原始简介", Bullets: []string{"完整机制，\n以及原文结果。"}}} {
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []Project{
		{Name: " "}, {Name: "项目", Description: "\x00"},
		{Name: "项目", Description: strings.Repeat("后", 1334)},
		{Name: "项目", Bullets: []string{" "}},
		{Name: "项目", Bullets: []string{strings.Repeat("后", 667)}},
		{Name: "项目", Bullets: []string{string([]byte{0xff})}},
		{Name: "项目", Bullets: make([]string, 21)},
	} {
		if p.Validate() == nil {
			t.Fatal("accepted invalid project content")
		}
	}
}
