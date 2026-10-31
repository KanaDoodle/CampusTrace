package config

import (
	"testing"
	"time"
)

func TestTaskQueueUsesKafkaAndRejectsRetiredModes(t *testing.T) {
	t.Setenv("TASK_QUEUE_MODE", "")
	if queueMode() != "kafka" {
		t.Fatal("Kafka is not default")
	}
	for _, mode := range []string{"typed", "legacy", "other"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("TASK_QUEUE_MODE", mode)
			defer func() {
				if recover() == nil {
					t.Fatal("retired queue mode accepted")
				}
			}()
			queueMode()
		})
	}
}
func TestTaskLeaseRetainsHostConfigCompatibility(t *testing.T) {
	t.Setenv("TASK_LEASE_SECONDS", "")
	t.Setenv("CLAIM_IDLE_SECONDS", "")
	if taskLeaseSeconds() != 45 {
		t.Fatal("default lease")
	}
	t.Setenv("CLAIM_IDLE_SECONDS", "60")
	if taskLeaseSeconds() != 60 {
		t.Fatal("legacy lease ignored")
	}
	t.Setenv("TASK_LEASE_SECONDS", "90")
	if taskLeaseSeconds() != 90 {
		t.Fatal("explicit task lease ignored")
	}
}

// Retired global settings must not block the per-lane Kafka worker policy.
func TestRetiredGlobalWorkerConcurrencyDoesNotControlKafkaWorkers(t *testing.T) {
	t.Setenv("TASK_QUEUE_MODE", "kafka")
	t.Setenv("WORKER_CONCURRENCY", "old-setting")
	t.Setenv("TASK_LEASE_SECONDS", "45")
	t.Setenv("TASK_TIMEOUT_SECONDS", "15")
	cfg := Load()
	if cfg.TaskLease != 45*time.Second || cfg.TaskTimeout != 15*time.Second {
		t.Fatal("task execution budgets changed")
	}
}
