package searchservice

import (
	"context"
	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

// Kafka and the operator HTTP path share this process's rebuild lock. Across
// replicas, generation checks and external document versions retain safety.
func (s *Server) ConsumeEvents(ctx context.Context, events []jobevent.Event) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status, err := s.Engine.Status(ctx)
	if err != nil {
		return err
	}
	docs := []a.Document{}
	for _, event := range events {
		if !event.Valid() {
			return a.ErrInvalid
		}
		if event.Kind == "snapshot" && event.Generation != status.Generation {
			continue
		}
		docs = append(docs, event.Document)
	}
	if len(docs) == 0 {
		return nil
	}
	return s.Engine.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: status.Generation, Documents: docs})
}
