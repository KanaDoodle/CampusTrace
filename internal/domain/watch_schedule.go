package domain

func SourceMinimumInterval(adapter string) int {
	switch adapter {
	case "xiaohongshu", "baidu", "meituan":
		return 1800
	default:
		return 300
	}
}

// WatchInterval is deliberately bounded and deterministic. Priority never
// bypasses a source's minimum interval or the failure cooldown.
func WatchInterval(w WatchTarget, minimum int, urgent bool) (int, string) {
	base := max(minimum, w.CheckInterval)
	ceiling := min(604800, base*8)
	if !w.Adaptive {
		return base, "FIXED"
	}
	if w.FailureRounds > 0 {
		return min(ceiling, base<<min(w.FailureRounds, 3)), "FAILURE_BACKOFF"
	}
	if w.Priority || urgent {
		return max(minimum, base/2), "PRIORITY"
	}
	if w.RoundChanged {
		return max(minimum, base/2), "RECENT_CHANGE"
	}
	if w.StableRounds >= 3 {
		return min(ceiling, base<<min(w.StableRounds/3, 3)), "UNCHANGED_BACKOFF"
	}
	return base, "BASE"
}

func (w *WatchTarget) RecordWatchRound(success bool) {
	if !success {
		w.FailureRounds = min(3, w.FailureRounds+1)
		w.StableRounds = 0
		return
	}
	w.FailureRounds = 0
	if w.RoundChanged {
		w.StableRounds = 0
	} else {
		w.StableRounds = min(9, w.StableRounds+1)
	}
}
