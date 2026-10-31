package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestTechnologyExpressionGrammar(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  [][]string
	}{
		{"C++", [][]string{{"c++"}}},
		{"Golang+c|CPP", [][]string{{"go"}, {"c", "c++"}}},
		{"c+++Go", [][]string{{"c++"}, {"go"}}},
		{"C++20|Rust+JavaScript", [][]string{{"c++", "rust"}, {"javascript"}}},
		{"c#+node.js", [][]string{{"c#"}, {"node.js"}}},
	} {
		got, valid := TechnologyExpression(tc.value)
		if !valid || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: got %v/%v, want %v", tc.value, got, valid, tc.want)
		}
	}
	for _, value := range []string{"", "go|", "|go", "go++java", "c+", "go||java", "(go|java)+c++", "go or rust"} {
		if _, valid := TechnologyExpression(value); valid {
			t.Fatal("unsupported/empty operand accepted", value)
		}
	}
}

func FuzzTechnologyExpressionCanonicalRoundTrip(f *testing.F) {
	for _, value := range []string{"c++", "go+c|c++", "go++java", "c+++go", "golang|C++20"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		groups, valid := TechnologyExpression(value)
		if !valid {
			return
		}
		var joined []string
		for _, group := range groups {
			if len(group) == 0 {
				t.Fatal("empty conjunction")
			}
			joined = append(joined, strings.Join(group, "|"))
		}
		again, valid := TechnologyExpression(strings.Join(joined, "+"))
		if !valid || !reflect.DeepEqual(groups, again) {
			t.Fatal("canonical expression changed meaning", value, groups, again)
		}
	})
}
