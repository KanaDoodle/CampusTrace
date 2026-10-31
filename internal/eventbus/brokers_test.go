package eventbus

import "testing"

func TestKafkaConfigRejectsInvalidBrokerPortsBeforeConnecting(t *testing.T) {
	for _, address := range []string{":9092", "kafka:", "kafka:http", "kafka:0", "kafka:65536", "kafka:-1", " kafka:9092"} {
		t.Run(address, func(t *testing.T) {
			if _, err := NewConfig(address, "test.jobs.v1", "test.jobs.dlq.v1", ""); err == nil {
				t.Fatalf("invalid Kafka address accepted: %q", address)
			}
		})
	}
}

func TestKafkaBrokerValidationAcceptsExplicitPortsAndIPv6(t *testing.T) {
	for _, addresses := range [][]string{{"kafka:9092"}, {"127.0.0.1:19092", "[::1]:9092"}} {
		if err := ValidateBrokers(addresses); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateBrokers(nil); err == nil {
		t.Fatal("missing broker accepted")
	}
}
