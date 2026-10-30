package searchapi

import "testing"

func TestTechnologyBoundaries(t *testing.T) {
	cases := []struct {
		text, term string
		want       bool
	}{{"C++ 网络开发", "c++", true}, {"C# Unity", "c++", false}, {"C++17 服务", "c++", true}, {"C#12 Unity", "c#", true}, {"Go1.25 后端", "go", true}, {"Google backend", "go", false}, {"Golang服务", "go", true}, {"C++/Go服务器", "Golang", true}, {"Python", "go", false}}
	for _, c := range cases {
		q := Query{Text: c.term, Mode: "ALL"}
		if got := q.Matches(Document{Text: c.text}); got != c.want {
			t.Errorf("%q / %q: got %v", c.text, c.term, got)
		}
	}
	q := Query{Text: "Go Redis", Mode: "ALL", Exclude: "实习"}
	if q.Matches(Document{Text: "Go Redis 实习"}) || q.Matches(Document{Text: "Go"}) {
		t.Fatal("all/exclude ignored")
	}
	q.Mode = "ANY"
	if !q.Matches(Document{Text: "Redis"}) {
		t.Fatal("any ignored")
	}
}
func TestDocumentVersionAndContentHash(t *testing.T) {
	d := Document{Dataset: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Visibility: "GLOBAL", JobType: "UNKNOWN", Version: 1, Text: "Go"}
	d.Hash = d.ContentHash()
	if !d.Valid() {
		t.Fatal("valid document rejected")
	}
	d.Version++
	if !d.Valid() {
		t.Fatal("version changed content hash")
	}
	d.Text = "C++"
	if d.Valid() {
		t.Fatal("changed content accepted with old hash")
	}
	d.Hash = d.ContentHash()
	d.Visibility = "PRIVATE"
	if d.Valid() {
		t.Fatal("private document without owner accepted")
	}
}
