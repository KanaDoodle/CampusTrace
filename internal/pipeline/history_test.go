package pipeline

import (
	"testing"
	"time"
)

func TestDispatchBackoffIsBoundedAndWorkResetsIt(t *testing.T) {
	delay := 200 * time.Millisecond
	for range 20 {
		next := dispatchDelay(delay, 0, false)
		if next < delay || next > 3*time.Second {
			t.Fatal(delay, next)
		}
		delay = next
	}
	if delay != 3*time.Second || dispatchDelay(delay, 1, false) != 200*time.Millisecond || dispatchDelay(200*time.Millisecond, 1, true) != 3*time.Second {
		t.Fatal("dispatch pacing lost responsiveness or failure backoff")
	}
}
