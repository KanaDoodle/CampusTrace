package rules

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"testing"
	"time"
)

func TestRepairIdentityFieldBoundaries(t *testing.T) {
	if Fingerprint("North Labs", "Backend", "FULL_TIME", []string{"Shanghai"}, "tech: go") == Fingerprint("North", "Labs Backend", "FULL_TIME", []string{"Shanghai"}, "tech: go") {
		t.Fatal("F02: company/title boundaries collapsed")
	}
	if Fingerprint("A", "B", "FULL_TIME", []string{"A|B"}, "x") == Fingerprint("A", "B", "FULL_TIME", []string{"A", "B"}, "x") {
		t.Fatal("F02: location boundaries collapsed")
	}
}
func TestRepairChinaDeadline(t *testing.T) {
	for _, tc := range []struct{ now, want string }{{"2026-09-29T23:00:00+08:00", "OPEN"}, {"2026-09-30T23:59:59+08:00", "OPEN"}, {"2026-10-01T00:00:00+08:00", "CLOSED"}, {"2026-10-01T00:01:00+08:00", "CLOSED"}} {
		t.Run(tc.now, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.now)
			o := d.Observation{ID: "o", PostingID: "p", Trust: "OFFICIAL", FetchStatus: "SUCCESS", ObservedAt: now.Add(-time.Hour), Text: "Apply now"}
			es := []d.Evidence{{ID: "a", ObservationID: "o", Claim: d.Claim{Type: "APPLY_ACTION", Value: "PRESENT", Confidence: 1, Excerpt: "Apply now"}}, {ID: "d", ObservationID: "o", Claim: d.Claim{Type: "DEADLINE", Value: "2026-09-30", Confidence: 1}}}
			if got := Status("j", []d.Observation{o}, es, now).Status; got != tc.want {
				t.Fatalf("F11: got %s want %s", got, tc.want)
			}
		})
	}
}
func TestRepairExplicitDeadline(t *testing.T) {
	for _, value := range []string{"2026-09-30T18:00:00+08:00", "2026-09-30 18:00 +08:00", "2026-09-30T10:00:00Z"} {
		c := d.Claim{Type: "DEADLINE", Value: value, Excerpt: value, Method: "RULE", Confidence: 1}
		if err := c.Validate(value); err != nil {
			t.Errorf("F11: explicit timezone rejected: %v", err)
		}
		now, _ := time.Parse(time.RFC3339, "2026-09-30T18:01:00+08:00")
		o := d.Observation{ID: "o", PostingID: "p", Trust: "OFFICIAL", FetchStatus: "SUCCESS", ObservedAt: now}
		es := []d.Evidence{{ObservationID: "o", Claim: c}}
		if Status("j", []d.Observation{o}, es, now).Status != "CLOSED" {
			t.Error("F11: explicit timezone not respected")
		}
	}
}
