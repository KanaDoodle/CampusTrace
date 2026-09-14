package domain

import "testing"

func TestWatchValidation(t *testing.T) {
	good := WatchInput{SourceID: "registered", CheckInterval: 3600, Enabled: true}
	if good.Validate() != nil {
		t.Fatal("valid watch rejected")
	}
	for _, n := range []int{-1, 0, 299, 604801} {
		bad := good
		bad.CheckInterval = n
		if bad.Validate() == nil {
			t.Fatalf("accepted interval %d", n)
		}
	}
	for _, s := range []string{"NONE", "SAVED", "IGNORED"} {
		if !ValidDisposition(s) {
			t.Fatal(s)
		}
	}
	if ValidDisposition("APPLIED") {
		t.Fatal("application state became preference")
	}
}
