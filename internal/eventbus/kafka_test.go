package eventbus

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

func TestKafkaIndependentGroupsAndFailedBatchReplay(t *testing.T) {
	brokers := os.Getenv("KAFKA_TEST_BROKERS")
	if brokers == "" {
		t.Skip("requires isolated Kafka")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	topic := "test-events-" + time.Now().Format("150405000000000")
	cfg, e := NewConfig(brokers, topic, topic+"-dlq", "")
	if e != nil {
		t.Fatal(e)
	}
	producer, e := NewProducer(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer producer.Close()
	if e = producer.EnsureTopics(ctx); e != nil {
		t.Fatal(e)
	}
	doc := a.Document{Dataset: strings.Repeat("a", 32), ID: strings.Repeat("b", 32), Version: 1, Visibility: "GLOBAL", JobType: "FULL_TIME", Title: "Go", Text: "Kafka"}
	doc.Hash = doc.ContentHash()
	event := jobevent.New(doc, nil, "")
	if e = producer.Publish(ctx, []jobevent.Event{event}); e != nil {
		t.Fatal(e)
	}
	var first, second atomic.Int32
	cfg.Group = topic + "-first"
	one, e := NewConsumer(cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Group = topic + "-second"
	two, e := NewConsumer(cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{}, 2)
	go func() {
		defer func() { one.Close(); done <- struct{}{} }()
		_ = one.Run(ctx, func(context.Context, []jobevent.Event) error {
			if first.Add(1) == 1 {
				return errors.New("effect unavailable")
			}
			return nil
		})
	}()
	go func() {
		defer func() { two.Close(); done <- struct{}{} }()
		_ = two.Run(ctx, func(context.Context, []jobevent.Event) error { second.Add(1); return nil })
	}()
	for first.Load() < 2 || second.Load() < 1 {
		if ctx.Err() != nil {
			t.Fatalf("groups did not independently replay: %d %d", first.Load(), second.Load())
		}
		time.Sleep(30 * time.Millisecond)
	}
	cancel()
	<-done
	<-done
}
