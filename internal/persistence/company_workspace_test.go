package persistence

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"testing"
	"time"
)

func TestDecisionDeadlinePreservesSourceDayAndRejectsUnusableObservations(t *testing.T) {
	now := time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC)
	o := d.Observation{ID: "o", PostingID: "p", ObservedAt: now, FetchStatus: "SUCCESS", ExtractionStatus: "COMPLETE", Timezone: "Asia/Shanghai"}
	evidence := []d.Evidence{{ObservationID: "o", Claim: d.Claim{Type: "DEADLINE", Value: "2026-10-12", Confidence: 1}}}
	deadline, date := decisionDeadline([]d.Observation{o}, evidence, now)
	if deadline == nil || date != "2026-10-12" || deadline.Format(time.RFC3339) != "2026-10-13T00:00:00+08:00" {
		t.Fatal(deadline, date)
	}
	evidence[0].Value = "2026-10-12T17:00:00+08:00"
	deadline, date = decisionDeadline([]d.Observation{o}, evidence, now)
	if deadline == nil || date != "" || deadline.Hour() != 17 {
		t.Fatal(deadline, date)
	}
	for _, at := range []time.Time{now.Add(2 * time.Minute), now.Add(-8 * 24 * time.Hour)} {
		o.ObservedAt = at
		if deadline, _ = decisionDeadline([]d.Observation{o}, evidence, now); deadline != nil {
			t.Fatal("unusable deadline", deadline)
		}
	}
	// A future-dated latest record must not revive an older posting's deadline.
	o.ObservedAt = now
	future := o
	future.ID = "future"
	future.ObservedAt = now.Add(time.Hour)
	if deadline, _ = decisionDeadline([]d.Observation{o, future}, evidence, now); deadline != nil {
		t.Fatal("older deadline revived", deadline)
	}
}
