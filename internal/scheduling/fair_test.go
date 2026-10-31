package scheduling

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestFairPermitsBoundOwnersQueueAndCancellation(t *testing.T) {
	p := New(1, 2, 1)
	release, e := p.Acquire(context.Background(), "batch")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, e := p.Acquire(ctx, "batch"); result <- e }()
	deadline := time.Now().Add(time.Second)
	for p.Stats().Waiting < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	small := make(chan func(), 1)
	go func() { r, _ := p.Acquire(context.Background(), "small"); small <- r }()
	for p.Stats().Waiting < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if _, e = p.Acquire(context.Background(), "overload"); !errors.Is(e, ErrFull) {
		t.Fatal(e)
	}
	cancel()
	if !errors.Is(<-result, context.Canceled) {
		t.Fatal("cancelled waiter executed")
	}
	release()
	select {
	case r := <-small:
		r()
	case <-time.After(time.Second):
		t.Fatal("small request starved")
	}
	if s := p.Stats(); s.Running != 0 || s.Waiting != 0 || s.PeakWaiting > 2 {
		t.Fatal(s)
	}
}

func TestCancelledContentionLeavesNoReservations(t *testing.T) {
	p := New(2, 8, 1)
	holdA, e := p.Acquire(context.Background(), "a")
	if e != nil {
		t.Fatal(e)
	}
	holdB, e := p.Acquire(context.Background(), "b")
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, e := p.Acquire(ctx, "a")
			if e == nil {
				release()
			}
		}()
	}
	cancel()
	wg.Wait()
	holdA()
	holdA()
	holdB()
	if stats := p.Stats(); stats.Running != 0 || stats.Waiting != 0 || stats.PeakWaiting > 8 {
		t.Fatal(stats)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.owners) != 0 || len(p.queues) != 0 || len(p.active) != 0 {
		t.Fatal("cancelled waiter retained")
	}
}
