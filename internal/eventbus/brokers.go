package eventbus

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// ValidateBrokers is shared by job events and background tasks. Configuration
// errors are reported before clients start retrying connections.
func ValidateBrokers(addresses []string) error {
	if len(addresses) == 0 {
		return fmt.Errorf("Kafka brokers are required")
	}
	for _, address := range addresses {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host == "" || strings.ContainsAny(host, " \t\r\n") {
			return fmt.Errorf("invalid Kafka broker address")
		}
		for _, digit := range port {
			if digit < '0' || digit > '9' {
				return fmt.Errorf("Kafka broker port must be 1..65535")
			}
		}
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 {
			return fmt.Errorf("Kafka broker port must be 1..65535")
		}
	}
	return nil
}
