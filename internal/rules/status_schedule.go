package rules

import (
	"slices"
	"sort"
	"time"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// NextStatusChange schedules only observable changes to Status. The continuously
// decaying ranking is computed on read and never needs periodic history writes.
func NextStatusChange(observations []d.Observation, evidence []d.Evidence, now time.Time) time.Time {
	os, es := Current(observations, evidence)
	var times []time.Time
	for _, o := range os {
		if o.Trust != "OFFICIAL" || o.FetchStatus != "SUCCESS" || o.ExtractionStatus == "FAILED" {
			continue
		}
		// Round up to SQL's microsecond precision. Freshness expires strictly
		// after seven days; deadlines are inclusive at their exact instant.
		times = append(times, ceilMicro(o.ObservedAt.Add(7*24*time.Hour).Add(time.Nanosecond)), ceilMicro(o.ObservedAt.Add(-time.Minute)))
		for _, e := range es {
			if e.ObservationID == o.ID && e.Type == "DEADLINE" && e.Confidence >= .8 {
				if deadline, err := d.DeadlineInstant(e.Value, o.Timezone); err == nil {
					times = append(times, ceilMicro(deadline))
				}
			}
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	current := Status("", os, es, now)
	for _, at := range times {
		if !at.After(now) {
			continue
		}
		future := Status("", os, es, at)
		if future.Status != current.Status || future.Reason != current.Reason || !slices.Equal(future.EvidenceIDs, current.EvidenceIDs) {
			return at
		}
	}
	return time.Time{}
}

func ceilMicro(t time.Time) time.Time {
	u := t.UTC().Truncate(time.Microsecond)
	if u.Before(t) {
		u = u.Add(time.Microsecond)
	}
	return u
}
