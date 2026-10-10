package rules

import (
	"testing"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

func TestStatusScheduleDeadlineAndFreshnessBoundaries(t *testing.T) {
	now, o, es := fixture()
	o.ObservedAt = o.ObservedAt.Add(123 * time.Nanosecond)
	expires := NextStatusChange([]d.Observation{o}, es, now)
	if !expires.Equal(o.ObservedAt.Add(7 * 24 * time.Hour).Truncate(time.Microsecond).Add(time.Microsecond)) {
		t.Fatal("freshness boundary lost precision", expires)
	}
	if Status("j", []d.Observation{o}, es, expires.Add(-time.Microsecond)).Status != "OPEN" || Status("j", []d.Observation{o}, es, expires).Status != "UNKNOWN" {
		t.Fatal("scheduled freshness transition is not real")
	}
	es = append(es, d.Evidence{ID: "deadline", ObservationID: o.ID, Claim: d.Claim{Type: "DEADLINE", Value: "2026-09-10", Confidence: 1}})
	want, _ := time.Parse(time.RFC3339, "2026-09-10T16:00:00Z")
	at := NextStatusChange([]d.Observation{o}, es, now)
	if !at.Equal(want) || Status("j", []d.Observation{o}, es, at).Status != "CLOSED" || Status("j", []d.Observation{o}, es, at.Add(-time.Microsecond)).Status != "OPEN" {
		t.Fatal("date-only Beijing deadline changed", at)
	}
	if NextStatusChange([]d.Observation{o}, es, at).Equal(at) {
		t.Fatal("elapsed deadline scheduled again")
	}
	es[len(es)-1].Value = "2026-09-10T18:00:00.000000123+08:00"
	want, _ = time.Parse(time.RFC3339Nano, "2026-09-10T10:00:00.000001Z")
	if at := NextStatusChange([]d.Observation{o}, es, now); !at.Equal(want) {
		t.Fatal("explicit deadline precision lost", at)
	}
}

func TestStatusScheduleIgnoresUnusableAndSupersededEvidence(t *testing.T) {
	now, original, es := fixture()
	for _, tc := range []struct {
		name string
		edit func(*d.Observation)
	}{
		{"failed fetch", func(o *d.Observation) { o.FetchStatus = "BLOCKED" }},
		{"failed extraction", func(o *d.Observation) { o.ExtractionStatus = "FAILED" }},
		{"non-official", func(o *d.Observation) { o.Trust = "THIRD_PARTY" }},
		{"already stale", func(o *d.Observation) { o.ObservedAt = now.Add(-8 * 24 * time.Hour) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := original
			tc.edit(&o)
			if at := NextStatusChange([]d.Observation{o}, es, now); !at.IsZero() {
				t.Fatal("scheduled a status that cannot change with time", at)
			}
		})
	}
	es = append(es, d.Evidence{ID: "weak", ObservationID: original.ID, Claim: d.Claim{Type: "DEADLINE", Value: "2026-09-10", Confidence: .5}})
	if at := NextStatusChange([]d.Observation{original}, es, now); !at.Equal(now.Add(7*24*time.Hour + time.Microsecond)) {
		t.Fatal("weak deadline affected scheduling", at)
	}
	latest := original
	latest.ID, latest.FetchStatus, latest.ObservedAt = "new", "TIMEOUT", now.Add(time.Second)
	if at := NextStatusChange([]d.Observation{original, latest}, es, now.Add(time.Second)); !at.IsZero() {
		t.Fatal("superseded evidence scheduled work", at)
	}
}

func TestStatusScheduleSkipsDeadlineWithoutOutcomeChange(t *testing.T) {
	now, o, es := fixture()
	o.Text += "\nApplications closed"
	es = append(es, d.Evidence{ID: "closed", ObservationID: o.ID, Claim: d.Claim{Type: "CLOSED_SIGNAL", Value: "CLOSED", Confidence: 1, Excerpt: "Applications closed"}}, d.Evidence{ID: "deadline", ObservationID: o.ID, Claim: d.Claim{Type: "DEADLINE", Value: "2026-09-10", Confidence: 1}})
	if at := NextStatusChange([]d.Observation{o}, es, now); !at.Equal(now.Add(7*24*time.Hour + time.Microsecond)) {
		t.Fatal("already-closed deadline caused redundant work", at)
	}
}
