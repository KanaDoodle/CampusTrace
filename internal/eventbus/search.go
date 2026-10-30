package eventbus

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
	"github.com/KanaDoodle/CampusTrace/internal/observability"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

// SearchTransport preserves the existing Core projection coordinator. Applying
// a batch now emits replay-only snapshots and checks a refreshed index fence;
// a Kafka producer acknowledgement alone can never clear Core's dirty rows.
type SearchTransport struct {
	HTTP     *a.Client
	Producer *Producer
	mu       sync.Mutex
	last     [32]byte
	sentAt   time.Time
}

func (s *SearchTransport) Status(ctx context.Context) (a.Status, error) { return s.HTTP.Status(ctx) }
func (s *SearchTransport) Query(ctx context.Context, q a.Query) (a.Result, error) {
	return s.HTTP.Query(ctx, q)
}
func (s *SearchTransport) Apply(ctx context.Context, b a.Batch) error {
	if b.Protocol != a.Protocol || len(b.Documents) == 0 || len(b.Documents) > a.MaxBatch {
		return a.ErrInvalid
	}
	events := []jobevent.Event{}
	hits := []a.Hit{}
	dataset := b.Documents[0].Dataset
	for _, doc := range b.Documents {
		if doc.Dataset != dataset || !doc.Valid() {
			return a.ErrInvalid
		}
		events = append(events, jobevent.Snapshot(doc, b.Generation, observability.TraceParent(ctx)))
		hits = append(hits, a.Hit{ID: doc.ID, Version: doc.Version, Hash: doc.Hash})
	}
	send, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	raw, _ := json.Marshal(struct {
		Generation string
		Hits       []a.Hit
	}{b.Generation, hits})
	identity := sha256.Sum256(raw)
	s.mu.Lock()
	if identity != s.last || time.Since(s.sentAt) >= 5*time.Second {
		if err := s.Producer.Publish(send, events); err != nil {
			s.mu.Unlock()
			return err
		}
		s.last = identity
		s.sentAt = time.Now()
	}
	s.mu.Unlock()
	// Wait briefly for the asynchronous consumer. A later coordinator turn retries
	// safely if the consumer is stopped or lagged.
	until := time.NewTimer(900 * time.Millisecond)
	defer until.Stop()
	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()
	for {
		out, err := s.HTTP.Applied(send, a.AppliedQuery{Protocol: a.Protocol, Generation: b.Generation, Dataset: dataset, Hits: hits})
		if err != nil {
			return err
		}
		if out.Ready {
			return nil
		}
		select {
		case <-send.Done():
			return send.Err()
		case <-until.C:
			return a.ErrPending
		case <-tick.C:
		}
	}
}
