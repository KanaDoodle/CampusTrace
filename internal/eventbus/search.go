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

// SearchTransport confirms refreshed index versions and repairs gaps with Kafka
// snapshots. A broker acknowledgement alone never clears Core's dirty rows.
type SearchTransport struct {
	HTTP     *a.Client
	Producer interface {
		Publish(context.Context, []jobevent.Event) error
	}
	mu     sync.Mutex
	last   [32]byte
	sentAt time.Time
}

func (s *SearchTransport) Status(ctx context.Context) (a.Status, error) { return s.HTTP.Status(ctx) }
func (s *SearchTransport) Query(ctx context.Context, q a.Query) (a.Result, error) {
	return s.HTTP.Query(ctx, q)
}
func (s *SearchTransport) Apply(ctx context.Context, b a.Batch) error {
	_, err := s.Sync(ctx, b, nil)
	return err
}

// Sync returns the IDs whose requested versions were visible. A nil repair
// map permits repair of every gap, as used during bootstrap and rebuild.
func (s *SearchTransport) Sync(ctx context.Context, b a.Batch, repair map[string]bool) ([]string, error) {
	if b.Protocol != a.Protocol || len(b.Documents) == 0 || len(b.Documents) > a.MaxBatch {
		return nil, a.ErrInvalid
	}
	query := a.AppliedQuery{Protocol: a.Protocol, Generation: b.Generation, Dataset: b.Documents[0].Dataset}
	for _, doc := range b.Documents {
		if doc.Dataset != query.Dataset || !doc.Valid() {
			return nil, a.ErrInvalid
		}
		query.Hits = append(query.Hits, a.Hit{ID: doc.ID, Version: doc.Version, Hash: doc.Hash})
	}
	send, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	confirmed, missing, err := s.visibility(send, query)
	if err != nil || len(missing) == 0 {
		return confirmed, err
	}
	var events []jobevent.Event
	for _, doc := range b.Documents {
		if missing[doc.ID] && (repair == nil || repair[doc.ID]) {
			events = append(events, jobevent.Snapshot(doc, b.Generation, observability.TraceParent(ctx)))
		}
	}
	if len(events) == 0 {
		return confirmed, a.ErrPending
	}
	if err := s.publishRepair(send, events); err != nil {
		return confirmed, err
	}
	// Wait briefly for the async consumer. Unconfirmed versions remain dirty.
	until := time.NewTimer(900 * time.Millisecond)
	defer until.Stop()
	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-send.Done():
			return confirmed, send.Err()
		case <-until.C:
			return confirmed, a.ErrPending
		case <-tick.C:
			next, missing, err := s.visibility(send, query)
			if err != nil {
				return confirmed, err
			}
			confirmed = next
			if len(missing) == 0 {
				return confirmed, nil
			}
		}
	}
}

func (s *SearchTransport) visibility(ctx context.Context, q a.AppliedQuery) ([]string, map[string]bool, error) {
	out, err := s.HTTP.Applied(ctx, q)
	if err != nil {
		return nil, nil, err
	}
	missing := make(map[string]bool, len(out.Missing))
	for _, id := range out.Missing {
		missing[id] = true
	}
	if !out.Ready && len(missing) == 0 {
		// An older Search returns only an aggregate flag. Check each version
		// before repairing it while the two components are being upgraded.
		for _, hit := range q.Hits {
			probe := q
			probe.Hits = []a.Hit{hit}
			one, err := s.HTTP.Applied(ctx, probe)
			if err != nil {
				return nil, nil, err
			}
			if !one.Ready {
				missing[hit.ID] = true
			}
		}
	}
	confirmed := make([]string, 0, len(q.Hits))
	for _, hit := range q.Hits {
		if !missing[hit.ID] {
			confirmed = append(confirmed, hit.ID)
		}
	}
	if out.Ready && len(missing) != 0 || len(missing)+len(confirmed) != len(q.Hits) {
		return nil, nil, a.ErrConflict
	}
	return confirmed, missing, nil
}

func (s *SearchTransport) publishRepair(ctx context.Context, events []jobevent.Event) error {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	raw, _ := json.Marshal(ids)
	identity := sha256.Sum256(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	if identity == s.last && time.Since(s.sentAt) < 5*time.Second {
		return nil
	}
	if err := s.Producer.Publish(ctx, events); err != nil {
		return err
	}
	s.last, s.sentAt = identity, time.Now()
	return nil
}

func (s *SearchTransport) ClaimRebuild(ctx context.Context, in a.RebuildRef) (a.Status, error) {
	return s.HTTP.ClaimRebuild(ctx, in)
}
func (s *SearchTransport) CommitRebuild(ctx context.Context, in a.RebuildCommit) (a.Status, error) {
	return s.HTTP.CommitRebuild(ctx, in)
}
func (s *SearchTransport) AckRebuild(ctx context.Context, in a.RebuildRef) (a.Status, error) {
	return s.HTTP.AckRebuild(ctx, in)
}

func (s *SearchTransport) Applied(ctx context.Context, q a.AppliedQuery) (a.AppliedResult, error) {
	return s.HTTP.Applied(ctx, q)
}
