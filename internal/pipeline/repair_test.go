package pipeline

import (
	"fmt"
	"testing"
	"time"
)

func TestRepairBackoffBounds(t *testing.T) {
	for _, n := range []int{-1, -100, 0, 1, 100, int(^uint(0) >> 1)} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("F04 panic: %v", v)
				}
			}()
			if b := Backoff(n); b < time.Second || b > 1536*time.Second {
				t.Error(b)
			}
		})
	}
}
