package searchservice

import (
	"context"
	"github.com/KanaDoodle/CampusTrace/internal/jobevent"
	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

// Kafka and the operator HTTP path share this process's rebuild lock. Across
// replicas, generation checks and external document versions retain safety.
func (s *Server) ConsumeEvents(ctx context.Context, events []jobevent.Event) error {
	status, err := s.Engine.Status(ctx)
	if err != nil {
		return err
	}

	batches := map[string][]a.Document{}
	order := []string{}
	for _, event := range events {
		if !event.Valid() {
			return a.ErrInvalid
		}
		generation := status.Generation
		if event.Kind == "snapshot" {
			generation = event.Generation
			allowed := generation == status.Generation
			if plan := status.Rebuild; plan != nil && (plan.Phase == "BUILDING" || plan.Phase == "CUTTING" || plan.Phase == "COMMITTED") {
				allowed = allowed || generation == plan.Source || generation == plan.Target
			}
			if !allowed {
				continue
			}
		}
		if _, ok := batches[generation]; !ok {
			order = append(order, generation)
		}
		batches[generation] = append(batches[generation], event.Document)
	}
	for _, generation := range order {
		if err := s.Engine.Apply(ctx, a.Batch{Protocol: a.Protocol, Generation: generation, Documents: batches[generation]}); err != nil {
			return err
		}
	}
	return nil
}
