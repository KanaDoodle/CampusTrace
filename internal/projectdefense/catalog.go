// Package projectdefense provides reviewed, immutable project evidence. The
// model selects IDs in this bundle; it cannot request filesystem paths or URLs.
package projectdefense

import (
	_ "embed"
	"encoding/json"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

//go:embed catalog.json
var raw []byte

type Evidence struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Start    int    `json:"start_line"`
	End      int    `json:"end_line"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Hash     string `json:"sha256"`
	FileHash string `json:"file_sha256"`
}
type Point struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
}
type Topic struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Question string     `json:"question"`
	Points   []Point    `json:"points"`
	Evidence []Evidence `json:"evidence"`
}
type Catalog struct {
	BaseCommit   string  `json:"base_commit"`
	SnapshotKind string  `json:"snapshot_kind"`
	Topics       []Topic `json:"topics"`
}

func Version() string { return d.Hash(string(raw)) }
func Load() Catalog {
	var c Catalog
	if err := json.Unmarshal(raw, &c); err != nil {
		panic("invalid embedded project evidence")
	}
	return c
}
func Find(id string) (Topic, bool) {
	for _, t := range Load().Topics {
		if t.ID == id {
			return t, true
		}
	}
	return Topic{}, false
}
