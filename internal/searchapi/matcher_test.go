package searchapi

import (
	"strings"
	"testing"
)

func TestPreparedMatcherMatchesHistoricalContract(t *testing.T) {
	texts := []string{"", "Go1.25 Redis C++17", "Google C#12", "校园岗位 上海 REDIS", "İ K ẞ ſ Σ σ ς", "a\xffB", "海" + strings.Repeat("A", 60000)}
	queries := []string{"", "redis", "Go", "c++", "C#", "i", "k", "ß", "s", "σ", "ς", "a\xffb", "海", "Go Redis", "校园 上海", "absent"}
	for _, mode := range []string{"ALL", "ANY", ""} {
		for _, term := range queries {
			for _, exclude := range []string{"", "redis", "Go", "实习", "ς"} {
				for _, company := range []string{"", "公司", "other"} {
					q := Query{Text: term, Exclude: exclude, Mode: mode, Company: company}
					m := q.PrepareMatcher()
					for i, text := range texts {
						d := Document{Title: "岗位", Company: "公司", Locations: []string{"上海", "杭州"}, JobType: "FULL_TIME", Text: text, Deleted: i%7 == 0}
						if got, want := m.Matches(d), referenceMatches(q, d); got != want {
							t.Fatalf("%+v / %q: got %v want %v", q, text, got, want)
						}
					}
				}
			}
		}
	}
	for _, q := range []Query{{City: "上海"}, {City: "杭州"}, {City: "北京"}, {JobType: "INTERNSHIP"}, {JobType: "FULL_TIME"}} {
		d := Document{Locations: []string{"上海", "杭州"}, JobType: "FULL_TIME"}
		if q.Matches(d) != referenceMatches(q, d) {
			t.Fatal(q)
		}
	}
}

func FuzzPreparedMatcher(f *testing.F) {
	for _, s := range []string{"Redis", "校园 GO", "İ K Σ", "x\xffY", "C++17"} {
		f.Add(s, "redis", "Go")
	}
	f.Fuzz(func(t *testing.T, text, term, exclude string) {
		if len(text) > 60000 || len(term) > 1000 || len(exclude) > 1000 {
			t.Skip()
		}
		for _, mode := range []string{"ALL", "ANY"} {
			q := Query{Text: term, Exclude: exclude, Mode: mode}
			m := q.PrepareMatcher()
			for _, s := range []string{text, "", text} {
				d := Document{Title: term, Company: "公司", Locations: []string{"上海", exclude}, Text: s}
				if m.Matches(d) != referenceMatches(q, d) {
					t.Fatalf("mismatch: %+v / %+v", q, d)
				}
			}
		}
	})
}

func BenchmarkCatalogMatching(b *testing.B) {
	d := Document{Title: "后端岗位", Company: "Synthetic", Locations: []string{"上海"}, Text: strings.Repeat("任职要求：熟悉 Golang、Redis、MySQL、分布式系统与网络编程。工作职责：开发可恢复的后端服务。 ", 8) + "loadterm1"}
	q := Query{Text: "absentdiagnosticterm", Mode: "ALL"}
	b.Run("historical", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = referenceMatches(q, d)
		}
	})
	b.Run("prepared", func(b *testing.B) {
		m := q.PrepareMatcher()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = m.Matches(d)
		}
	})
}
