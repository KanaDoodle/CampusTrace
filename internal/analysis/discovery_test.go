package analysis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/KanaDoodle/CampusTrace/internal/config"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	rpc "github.com/KanaDoodle/KanaRPC"
	"github.com/KanaDoodle/KanaRPC/discovery"
)

func TestKanaRPCDiscoveryRebuildsClosedWatch(t *testing.T) {
	if os.Getenv("CAMPUS_INTEGRATION") != "1" {
		t.Skip("requires owned etcd")
	}
	root, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry, err := discovery.New(root, discovery.Config{Endpoints: config.Load().Endpoints(), Namespace: "/ct-discovery-" + d.ID(), MaxResolvers: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		if err := registry.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	_, err = registry.Register(root, "analysis", rpc.Endpoint{ID: "fixture", Address: "127.0.0.1:19091", Transport: rpc.TCP}, 10)
	if err != nil {
		t.Fatal(err)
	}
	source, err := newAnalysisDiscovery(root, registry, "analysis")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		select {
		case <-source.done:
		case <-time.After(2 * time.Second):
			t.Error("discovery refresh did not join")
		}
	}()
	for i := 0; i < 3; i++ {
		previous := source.current.Load()
		before := source.Snapshot()
		ctx, stop := context.WithTimeout(root, time.Second)
		err := previous.Close(ctx)
		stop()
		if err != nil {
			t.Fatal(err)
		}
		awaitRPC(t, func() bool {
			return source.current.Load() != previous && source.Snapshot().Ready && registry.Stats().Resolvers == 1
		})
		after := source.Snapshot()
		if after.ClusterID != before.ClusterID || after.Revision < before.Revision || len(after.Endpoints) != 1 || after.Endpoints[0].ID != "fixture" {
			t.Fatal("replacement lost discovery lineage", before, after)
		}
	}
	cancel()
	select {
	case <-source.done:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh goroutine retained")
	}
	ctx, stop := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop()
	if err := registry.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if stats := registry.Stats(); !stats.Closed || stats.Resolvers != 0 || stats.Registrations != 0 {
		t.Fatal(stats)
	}
}
