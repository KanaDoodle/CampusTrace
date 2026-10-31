package jobevent

import (
	"encoding/json"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
	"strings"
	"testing"
)

func TestVersionedEnvelope(t *testing.T) {
	doc := a.Document{Dataset: strings.Repeat("a", 32), ID: strings.Repeat("b", 32), Visibility: "PRIVATE", Owner: strings.Repeat("c", 32), Version: 1, Company: "合成公司", Title: "开发", Text: "Go", JobType: "FULL_TIME"}
	doc.Hash = doc.ContentHash()
	event := New(doc, nil, "")
	raw, _ := json.Marshal(event)
	if !event.Valid() {
		t.Fatal("valid event rejected")
	}
	if _, e := Decode(raw); e != nil {
		t.Fatal(e)
	}
	doc.Version = 2
	doc.Text = "C++"
	doc.Hash = doc.ContentHash()
	changed := New(doc, &event.Document, "")
	if changed.ID == event.ID || len(changed.Changes) != 1 || changed.Changes[0] != "content" {
		t.Fatal(changed)
	}
	if _, e := Decode(append(raw, []byte(` {}`)...)); e == nil {
		t.Fatal("trailing data accepted")
	}
	event.Schema = 2
	raw, _ = json.Marshal(event)
	if _, e := Decode(raw); e == nil {
		t.Fatal("unsupported schema accepted")
	}
	snapshot := Snapshot(doc, "index:new", "")
	if !snapshot.Valid() || snapshot.Kind != "snapshot" {
		t.Fatal(snapshot)
	}
}
