package agent

import (
	"strings"
	"testing"
)

func TestNarrationOutputRedactionUsesCumulativeTextAndHoldback(t *testing.T) {
	input := strings.Repeat("safe ", 100) + "手机号13800138000，张三"
	partial := narrationSafe(input, "张三", true)
	if strings.Contains(partial, "13800138000") || strings.Contains(partial, "张三") {
		t.Fatal(partial)
	}
	full := narrationSafe(input, "张三", false)
	if strings.Contains(full, "13800138000") || strings.Contains(full, "张三") {
		t.Fatal(full)
	}
	if narrationSafe("short partial", "", true) != "" {
		t.Fatal("sensitive tail was streamed before it could be checked")
	}
}

func TestNarrationSnapshotBoundsAfterRedaction(t *testing.T) {
	for _, partial := range []bool{false, true} {
		if value, err := narrationSnapshot(strings.Repeat("x", 3000), "x", partial); err != ErrNarrationOutput || value != "" {
			t.Fatalf("expanded redaction escaped snapshot cap: bytes=%d error=%v", len(value), err)
		}
	}
}
