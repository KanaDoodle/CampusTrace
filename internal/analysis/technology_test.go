package analysis

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestTechnologyExtractionUsesOnlySourceLanguages(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"cpp", "熟悉C++开发", []string{"c++"}},
		{"c_family", "熟悉C/C++开发", []string{"c|c++"}},
		{"exact_choice", "掌握Go或Rust任一种语言", []string{"go|rust"}},
		{"required_choice", "必须掌握Go或Rust任一种语言", []string{"REQUIRED:go|rust"}},
		{"conjunction", "必须掌握Go和C++", []string{"REQUIRED:go+c++"}},
		{"preference_scope", "掌握Go，C++经验优先", []string{"go", "c++"}},
		{"section_and_bonus", "任职要求：熟悉Go；C++经验优先", []string{"REQUIRED:go", "c++"}},
		{"responsibilities", "工作职责：使用Go编写服务", []string{"go"}},
		{"not_required", "无需Go经验", nil},
		{"open_examples", "任职要求：熟悉Java、C++等至少一门语言", []string{"java|c++"}},
		{"term_boundaries", "Django governance Caching javascriptish", nil},
		{"label_compatibility", "  tech: REQUIRED:go|java", []string{"REQUIRED:go|java"}},
		{"multiline_context", "任职要求\n熟悉Go\n加分项\n掌握Rust\n工作职责\n熟悉Java", []string{"REQUIRED:go", "rust", "java"}},
		{"family_in_conjunction", "必须熟悉Go和C/C++", []string{"REQUIRED:go+c|c++"}},
		{"mixed_logic", "必须熟悉Go或Rust并掌握C++", []string{"UNCERTAIN:go|rust+c++"}},
		{"negation_scope", "Must know Go but C++ is not required", []string{"REQUIRED:go"}},
		{"open_example_scope", "任职要求：熟悉至少一门语言，包括但不限于Java、C++", []string{"java|c++"}},
		{"future_learning", "Must be willing to learn Go", []string{"go"}},
		{"empty_clauses", ";；，,。\n熟悉C#；；", []string{"c#"}},
		{"unknown_alternative", "必须熟悉Go或Zig任一种语言", []string{"UNCERTAIN:go"}},
		{"unknown_first_alternative", "必须熟悉Zig或Go任一种语言", []string{"UNCERTAIN:go"}},
		{"group_of_conditions", "任职要求\n符合以下任意两条：\n了解C/C++\n具备以下条件尤佳，但不是必须：\n熟悉Go", []string{"UNCERTAIN:c|c++", "go"}},
		{"foundation_requirement", "任职要求\nC++基础扎实", []string{"REQUIRED:c++"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims, err := Extract(context.Background(), tc.text)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, claim := range claims {
				if err := claim.Validate(tc.text); err != nil {
					t.Fatal("source binding failed", err)
				}
				if claim.Type == "TECH_STACK" {
					got = append(got, claim.Value)
				}
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %v, want %v", got, want)
			}
		})
	}
}

func TestTechnologyExtractionRejectsOversizedRequirementInsteadOfTruncating(t *testing.T) {
	if _, err := Extract(context.Background(), "必须掌握Go"+strings.Repeat("需求", 400)); err == nil {
		t.Fatal("oversized requirement silently truncated")
	}
}

func FuzzTechnologyExtractionKeepsSourceBinding(f *testing.F) {
	for _, text := range []string{"必须熟悉Go和C++", "Go或Rust任一种", ";；，,。", "\xffC++\n任职要求：熟悉C/C++", "Must know Go but C++ is not required"} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 4096 {
			t.Skip()
		}
		claims, err := extractTechnologies(text)
		if err != nil {
			return
		}
		for _, claim := range claims {
			if err := claim.Validate(text); err != nil {
				t.Fatal("unbound or invalid claim", err)
			}
			value := strings.TrimPrefix(strings.TrimPrefix(claim.Value, "REQUIRED:"), "UNCERTAIN:")
			groups, valid := d.TechnologyExpression(value)
			if !valid {
				t.Fatal("extractor produced invalid expression", value)
			}
			present := map[string]bool{}
			for _, token := range languages(claim.Excerpt) {
				present[token.name] = true
			}
			for _, group := range groups {
				for _, name := range group {
					if !present[name] {
						t.Fatal("invented a language absent from the excerpt", name, claim.Excerpt)
					}
				}
			}
		}
	})
}
