package analysis

import (
	"context"
	"sync/atomic"
	"time"

	rpc "github.com/KanaDoodle/KanaRPC"
	"github.com/KanaDoodle/KanaRPC/discovery"
)

// analysisDiscovery renews an expired watch through the SDK's public API. A
// live watch can stop proving freshness without closing its transport. Never
// extend the old snapshot's TTL: obtain a fresh linearizable snapshot instead.
// The registry admits at most two resolvers: current plus one replacement.
type analysisDiscovery struct {
	registry *discovery.Registry
	service  string
	current  atomic.Pointer[discovery.Resolver]
	done     chan struct{}
}

func newAnalysisDiscovery(ctx context.Context, registry *discovery.Registry, service string) (*analysisDiscovery, error) {
	first, err := registry.Watch(ctx, service, rpc.TCP)
	if err != nil {
		return nil, err
	}
	source := &analysisDiscovery{registry: registry, service: service, done: make(chan struct{})}
	source.current.Store(first)
	go source.run(ctx)
	return source, nil
}
func (s *analysisDiscovery) Snapshot() discovery.Snapshot { return s.current.Load().Snapshot() }
func (s *analysisDiscovery) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		before := s.Snapshot()
		if before.Ready {
			continue
		}
		next, err := s.registry.Watch(ctx, s.service, rpc.TCP)
		if err != nil {
			continue
		} // SDK bounds each read; retry at the next tick.
		after := next.Snapshot()
		compatible := after.Ready && (before.ClusterID == 0 || before.ClusterID == after.ClusterID) && after.Revision >= before.Revision
		old := next
		if compatible {
			old = s.current.Swap(next)
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = old.Close(cleanup)
		cancel()
	}
}
