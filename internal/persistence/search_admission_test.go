package persistence

import (
	"context"
	"errors"
	"testing"
	"time"
)

func occupiedFallback(t *testing.T) *searchReads {
	t.Helper()
	g := new(searchReads)
	if !g.enter(true) || !g.enter(true) || g.enter(true) {
		t.Fatal("fallback capacity changed")
	}
	return g
}

func TestFallbackAdmissionAbsorbsShortBurstWithoutExtraSQLCapacity(t *testing.T) {
	g := occupiedFallback(t)
	released := make(chan struct{})
	go func() { time.Sleep(10 * time.Millisecond); g.leave(true); close(released) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	waited, err := g.admitFallback(ctx)
	<-released
	if err != nil || !waited || len(g.fallback) != 2 {
		t.Fatal(waited, err, len(g.fallback))
	}
	g.leave(true)
	g.leave(true)
	if len(g.fallback) != 0 {
		t.Fatal("permit leaked")
	}
}

func TestFallbackAdmissionHasBoundedWaitAndHonorsCallerCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		g := occupiedFallback(t)
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			time.AfterFunc(5*time.Millisecond, cancel)
		}
		started := time.Now()
		waited, err := g.admitFallback(ctx)
		cancel()
		want := ErrSearchBusy
		if cancelled {
			want = context.Canceled
		}
		if !waited || !errors.Is(err, want) || len(g.fallback) != 2 || time.Since(started) > time.Second {
			t.Fatal(waited, err, len(g.fallback))
		}
		g.leave(true)
		g.leave(true)
	}
	// A cancelled caller must not acquire an otherwise free permit.
	g := new(searchReads)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := g.admitFallback(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	g.init()
	if len(g.fallback) != 0 {
		t.Fatal("cancelled caller occupied a permit")
	}
}
