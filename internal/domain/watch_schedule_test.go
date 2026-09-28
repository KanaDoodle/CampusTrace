package domain

import "testing"

func TestWatchScheduleBoundsAndFailurePriority(t *testing.T) {
	w := WatchTarget{WatchInput: WatchInput{CheckInterval: 3600, Adaptive: true}}
	for i := 0; i < 9; i++ {
		w.RecordWatchRound(true)
	}
	if n, r := WatchInterval(w, 300, false); n != 28800 || r != "UNCHANGED_BACKOFF" {
		t.Fatal(n, r)
	}
	w.Priority = true
	if n, _ := WatchInterval(w, 300, false); n != 1800 {
		t.Fatal(n)
	}
	w.RecordWatchRound(false)
	if n, r := WatchInterval(w, 300, true); n != 7200 || r != "FAILURE_BACKOFF" {
		t.Fatal(n, r)
	}
	w.RoundChanged = true
	w.RecordWatchRound(true)
	if w.StableRounds != 0 || w.FailureRounds != 0 {
		t.Fatal(w)
	}
	if n, _ := WatchInterval(w, 1800, false); n != 1800 {
		t.Fatal("source floor bypassed", n)
	}
	w.CheckInterval = 604800
	w.Priority = false
	w.RoundChanged = false
	w.StableRounds = 9
	if n, _ := WatchInterval(w, 300, false); n != 604800 {
		t.Fatal("maximum bypassed", n)
	}
	w.Adaptive = false
	if n, r := WatchInterval(w, 300, true); n != 604800 || r != "FIXED" {
		t.Fatal(n, r)
	}
}
