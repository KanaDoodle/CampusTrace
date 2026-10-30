package eventbus

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kmsg"
)

// EnsureTopics is an explicit operator/test action, never an API request side effect.
func (p *Producer) EnsureTopics(ctx context.Context) error {
	request := kmsg.NewPtrCreateTopicsRequest()
	for _, topic := range []string{p.Config.Topic, p.Config.DLQ} {
		t := kmsg.NewCreateTopicsRequestTopic()
		t.Topic = topic
		t.NumPartitions = 3
		t.ReplicationFactor = 1
		for name, value := range map[string]string{"retention.ms": "604800000", "max.message.bytes": "1048576", "cleanup.policy": "delete"} {
			config := kmsg.NewCreateTopicsRequestTopicConfig()
			config.Name = name
			config.Value = &value
			t.Configs = append(t.Configs, config)
		}
		request.Topics = append(request.Topics, t)
	}
	response, e := p.client.Request(ctx, request)
	if e != nil {
		return e
	}
	for _, topic := range response.(*kmsg.CreateTopicsResponse).Topics {
		if topic.ErrorCode != 0 && topic.ErrorCode != 36 {
			return fmt.Errorf("Kafka topic creation failed (%d)", topic.ErrorCode)
		}
	}
	return nil
}
