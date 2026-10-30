// Package eventbus contains Kafka transport, with manual commit only after
// consumer effects are durable. It has no dependency on Core persistence.
package eventbus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Brokers           []string
	Topic, DLQ, Group string
}

func FromEnv(group string) (Config, error) {
	return NewConfig(os.Getenv("KAFKA_BROKERS"), env("KAFKA_JOB_TOPIC", "campustrace.jobs.v1"), env("KAFKA_DLQ_TOPIC", "campustrace.jobs.dlq.v1"), group)
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func NewConfig(brokers, topic, dlq, group string) (Config, error) {
	c := Config{strings.Split(brokers, ","), topic, dlq, group}
	valid := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,199}$`)
	if !valid.MatchString(topic) || !valid.MatchString(dlq) || topic == dlq || group != "" && !valid.MatchString(group) {
		return c, fmt.Errorf("invalid Kafka configuration")
	}
	for _, address := range c.Brokers {
		host, port, e := net.SplitHostPort(address)
		if e != nil || host == "" || port == "" {
			return c, fmt.Errorf("invalid Kafka broker address")
		}
	}
	return c, nil
}

type Producer struct {
	client *kgo.Client
	Config Config
}

func base(c Config) []kgo.Opt {
	return []kgo.Opt{kgo.SeedBrokers(c.Brokers...), kgo.ClientID("campustrace-events"), kgo.RequiredAcks(kgo.AllISRAcks()), kgo.MaxProduceRequestsInflightPerBroker(1), kgo.ProducerLinger(10 * time.Millisecond), kgo.MaxBufferedRecords(128), kgo.MaxBufferedBytes(8 << 20), kgo.ProducerBatchMaxBytes(1 << 20), kgo.RecordDeliveryTimeout(5 * time.Second), kgo.DialTimeout(2 * time.Second), kgo.RequestTimeoutOverhead(2 * time.Second)}
}
func NewProducer(c Config) (*Producer, error) {
	client, e := kgo.NewClient(base(c)...)
	if e != nil {
		return nil, e
	}
	return &Producer{client, c}, nil
}
func (p *Producer) Close() { p.client.Close() }
func (p *Producer) Publish(ctx context.Context, events []jobevent.Event) (err error) {
	if len(events) == 0 || len(events) > 64 {
		return fmt.Errorf("invalid event batch")
	}
	records := make([]*kgo.Record, 0, len(events))
	spans := make([]trace.Span, 0, len(events))
	defer func() {
		for _, span := range spans {
			observability.End(span, err)
		}
	}()
	for _, event := range events {
		raw, e := json.Marshal(event)
		if e != nil || !event.Valid() || len(raw) > jobevent.MaxBytes-1024 {
			return fmt.Errorf("invalid job event")
		}
		// Each durable event keeps its own parent even in a mixed-request batch.
		send, span := observability.Producer(observability.Extract(ctx, event.TraceParent), "kafka.publish")
		spans = append(spans, span)
		records = append(records, &kgo.Record{Topic: p.Config.Topic, Key: []byte(event.Key()), Value: raw, Headers: []kgo.RecordHeader{{Key: "traceparent", Value: []byte(observability.TraceParent(send))}}})
	}
	return p.client.ProduceSync(ctx, records...).FirstErr()
}
func (p *Producer) quarantine(ctx context.Context, r *kgo.Record, reason string) error {
	h := sha256.Sum256(r.Value)
	body, _ := json.Marshal(map[string]any{"schema": 1, "source_topic": r.Topic, "consumer_group": p.Config.Group, "partition": r.Partition, "offset": r.Offset, "sha256": hex.EncodeToString(h[:]), "reason": reason})
	return p.client.ProduceSync(ctx, &kgo.Record{Topic: p.Config.DLQ, Key: []byte(fmt.Sprintf("%s/%d/%d", r.Topic, r.Partition, r.Offset)), Value: body}).FirstErr()
}

type Consumer struct {
	client   *kgo.Client
	producer *Producer
	Metrics  *observability.Metrics
	Config   Config
}

func consumerClient(c Config) (*kgo.Client, error) {
	opts := append(base(c), kgo.ConsumerGroup(c.Group), kgo.ConsumeTopics(c.Topic), kgo.DisableAutoCommit(), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()), kgo.BlockRebalanceOnPoll(), kgo.FetchMaxBytes(4<<20), kgo.FetchMaxPartitionBytes(1<<20), kgo.SessionTimeout(30*time.Second), kgo.RebalanceTimeout(60*time.Second))
	return kgo.NewClient(opts...)
}
func NewConsumer(c Config, metrics *observability.Metrics) (*Consumer, error) {
	if c.Group == "" {
		return nil, fmt.Errorf("consumer group required")
	}
	client, e := consumerClient(c)
	if e != nil {
		return nil, e
	}
	p, e := NewProducer(c)
	if e != nil {
		client.Close()
		return nil, e
	}
	if metrics == nil {
		metrics = observability.New()
	}
	return &Consumer{client, p, metrics, c}, nil
}
func (c *Consumer) Close() { c.client.Close(); c.producer.Close() }

// A failed effect or commit leaves the consumer group and starts a new session
// at committed offsets. This avoids seeking buffered fetches across rebalances.
func (c *Consumer) Run(ctx context.Context, apply func(context.Context, []jobevent.Event) error) error {
	for ctx.Err() == nil {
		failure := c.session(ctx, apply)
		if ctx.Err() != nil {
			return nil
		}
		if failure != nil {
			c.Metrics.Add("event_consumer_errors_total", 1)
			slog.WarnContext(ctx, "job event consumer retrying", "group", c.Config.Group)
		}
		c.client.Close()
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
		client, e := consumerClient(c.Config)
		if e != nil {
			return e
		}
		c.client = client
	}
	return nil
}
func (c *Consumer) session(ctx context.Context, apply func(context.Context, []jobevent.Event) error) error {
	for ctx.Err() == nil {
		poll, cancel := context.WithTimeout(ctx, time.Second)
		fetches := c.client.PollRecords(poll, 64)
		cancel()
		records := fetches.Records()
		if len(records) == 0 {
			c.client.AllowRebalance()
			for _, failure := range fetches.Errors() {
				if !errors.Is(failure.Err, context.DeadlineExceeded) && !errors.Is(failure.Err, context.Canceled) {
					c.Metrics.Add("event_fetch_errors_total", 1)
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(250 * time.Millisecond):
					}
					break
				}
			}
			continue
		}
		process, stop := context.WithTimeout(ctx, 20*time.Second)
		events := []jobevent.Event{}
		parent := ""
		var failure error
		for _, record := range records {
			event, e := jobevent.Decode(record.Value)
			if e != nil || string(record.Key) != event.Key() {
				if e = c.producer.quarantine(process, record, "INVALID_EVENT"); e != nil {
					failure = e
					break
				}
				c.Metrics.Add("event_dlq_total", 1)
				continue
			}
			if parent == "" {
				parent = event.TraceParent
				for _, header := range record.Headers {
					if header.Key == "traceparent" && observability.ValidTraceParent(string(header.Value)) {
						parent = string(header.Value)
						break
					}
				}
			}
			events = append(events, event)
		}
		if failure == nil && len(events) > 0 {
			child, span := observability.Consumer(process, "job.event.consume", parent)
			failure = apply(child, events)
			observability.End(span, failure)
		}
		if failure == nil {
			failure = c.client.CommitRecords(process, records...)
		}
		stop()
		c.client.AllowRebalance()
		if failure != nil {
			return failure
		}
		c.Metrics.Add("event_consumer_records_total", float64(len(records)))
	}
	return nil
}
