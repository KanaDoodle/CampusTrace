package taskqueue

import (
	"database/sql"
	"testing"
	"time"
)

func TestTaskBrokerRejectsInvalidAddressesBeforeStartingClients(t *testing.T) {
	for _, address := range []string{":9092", "kafka:", "kafka:http", "kafka:0", "kafka:65536", " kafka:9092"} {
		cfg := Config{DB: &sql.DB{}, Brokers: []string{address}, Prefix: "test.tasks", Namespace: "test", Lanes: []string{"ASSESS"}, Capacity: 1, OwnerCapacity: 1, Lease: time.Second}
		broker, err := New(cfg)
		if err == nil {
			broker.Close()
			t.Fatalf("invalid task broker accepted: %q", address)
		}
	}
}
