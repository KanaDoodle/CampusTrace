// Package jobevent is the versioned job-change contract shared by independent
// consumers. It contains derived job data, never candidate profiles or keys.
package jobevent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/observability"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

const Schema = 1
const MaxBytes = 1 << 20

type Event struct {
	Schema         int        `json:"schema"`
	ID             string     `json:"event_id"`
	Kind           string     `json:"kind"`
	OccurredAt     time.Time  `json:"occurred_at"`
	TraceParent    string     `json:"traceparent,omitempty"`
	Generation     string     `json:"generation,omitempty"`
	PreviousStatus string     `json:"previous_status,omitempty"`
	Changes        []string   `json:"changes"`
	Document       a.Document `json:"document"`
}

func (e Event) Key() string { return e.Document.Dataset + "/" + e.Document.ID }
func (e Event) Identity() string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d/%s/%d/%s/%s", Schema, e.Key(), e.Document.Version, e.Kind, e.Generation)))
	return hex.EncodeToString(h[:])
}
func (e Event) Valid() bool {
	if e.Schema != Schema || !e.Document.Valid() || e.ID != e.Identity() || e.OccurredAt.IsZero() || !observability.ValidTraceParent(e.TraceParent) {
		return false
	}
	if e.Kind == "snapshot" {
		return e.Generation != "" && len(e.Generation) <= 160
	}
	if e.Generation != "" || e.Kind != "created" && e.Kind != "updated" && e.Kind != "deleted" {
		return false
	}
	if (e.Kind == "deleted") != e.Document.Deleted {
		return false
	}
	for _, change := range e.Changes {
		if change != "content" && change != "metadata" && change != "status" && change != "visibility" {
			return false
		}
	}
	return len(e.Changes) <= 4 && len(e.PreviousStatus) <= 64
}
func Decode(raw []byte) (Event, error) {
	var e Event
	if len(raw) == 0 || len(raw) > MaxBytes {
		return e, a.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&e) != nil || decoder.Decode(new(any)) != io.EOF || !e.Valid() {
		return e, a.ErrInvalid
	}
	return e, nil
}
func New(doc a.Document, old *a.Document, parent string) Event {
	e := Event{Schema: Schema, Kind: "updated", Document: doc, OccurredAt: time.Now().UTC(), TraceParent: parent, Changes: []string{}}
	if old == nil || old.Deleted {
		e.Kind = "created"
	}
	if doc.Deleted {
		e.Kind = "deleted"
	}
	if old != nil {
		e.PreviousStatus = old.Status
		if old.Text != doc.Text {
			e.Changes = append(e.Changes, "content")
		}
		if old.Company != doc.Company || old.Title != doc.Title || old.JobType != doc.JobType || !slices.Equal(old.Locations, doc.Locations) {
			e.Changes = append(e.Changes, "metadata")
		}
		if old.Status != doc.Status {
			e.Changes = append(e.Changes, "status")
		}
		if old.Visibility != doc.Visibility || old.Owner != doc.Owner {
			e.Changes = append(e.Changes, "visibility")
		}
	}
	e.ID = e.Identity()
	return e
}
func Snapshot(doc a.Document, generation, parent string) Event {
	e := New(doc, nil, parent)
	e.Kind = "snapshot"
	e.Generation = generation
	e.ID = e.Identity()
	return e
}
