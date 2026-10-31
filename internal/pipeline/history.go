package pipeline

import "time"

func dispatchDelay(previous time.Duration, work int, failed bool) time.Duration {
	if work > 0 && !failed {
		return 200 * time.Millisecond
	}
	if failed {
		return 3 * time.Second
	}
	return min(3*time.Second, max(200*time.Millisecond, previous*2))
}
