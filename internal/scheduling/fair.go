// Package scheduling provides bounded, cancellable round-robin execution permits.
// It does not retain credentials or run user code. A permit covers one quantum.
package scheduling

import (
	"context"
	"errors"
	"sync"
)

var ErrFull = errors.New("scheduler waiting capacity exhausted")

type waiter struct {
	owner   string
	ready   chan struct{}
	granted bool
	removed bool
}
type Pool struct {
	mu                          sync.Mutex
	limit, maxWaiting, perOwner int
	running, waiting            int
	peak                        int
	active                      map[string]int
	queues                      map[string][]*waiter
	owners                      []string
}

func New(concurrency, maxWaiting, perOwner int) *Pool {
	if concurrency < 1 || maxWaiting < 1 || perOwner < 1 {
		panic("positive scheduler limits required")
	}
	return &Pool{limit: concurrency, maxWaiting: maxWaiting, perOwner: perOwner, active: map[string]int{}, queues: map[string][]*waiter{}}
}
func (p *Pool) Acquire(ctx context.Context, owner string) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if owner == "" {
		return nil, errors.New("scheduler owner required")
	}
	p.mu.Lock()
	if p.waiting >= p.maxWaiting {
		p.mu.Unlock()
		return nil, ErrFull
	}
	w := &waiter{owner: owner, ready: make(chan struct{})}
	if len(p.queues[owner]) == 0 {
		p.owners = append(p.owners, owner)
	}
	p.queues[owner] = append(p.queues[owner], w)
	p.waiting++
	if p.waiting > p.peak {
		p.peak = p.waiting
	}
	p.dispatch()
	p.mu.Unlock()
	select {
	case <-w.ready:
		if e := ctx.Err(); e != nil {
			p.release(owner)
			return nil, e
		}
		var once sync.Once
		return func() { once.Do(func() { p.release(owner) }) }, nil
	case <-ctx.Done():
		p.mu.Lock()
		granted := w.granted
		if !granted && !w.removed {
			w.removed = true
			p.waiting--
			q := p.queues[owner]
			for i, entry := range q {
				if entry == w {
					q = append(q[:i], q[i+1:]...)
					break
				}
			}
			p.queues[owner] = q
			if len(q) == 0 {
				delete(p.queues, owner)
				owners := p.owners[:0]
				for _, id := range p.owners {
					if id != owner {
						owners = append(owners, id)
					}
				}
				p.owners = owners
			}
			p.dispatch()
		}
		p.mu.Unlock()
		if granted {
			p.release(owner)
		}
		return nil, ctx.Err()
	}
}
func (p *Pool) release(owner string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running--
	p.active[owner]--
	if p.active[owner] == 0 {
		delete(p.active, owner)
	}
	p.dispatch()
}
func (p *Pool) dispatch() {
	for p.running < p.limit && len(p.owners) > 0 {
		scanned := len(p.owners)
		progress := false
		for i := 0; i < scanned; i++ {
			owner := p.owners[0]
			p.owners = p.owners[1:]
			q := p.queues[owner]
			for len(q) > 0 && q[0].removed {
				q = q[1:]
			}
			if len(q) == 0 {
				delete(p.queues, owner)
				continue
			}
			if p.active[owner] >= p.perOwner {
				p.queues[owner] = q
				p.owners = append(p.owners, owner)
				continue
			}
			w := q[0]
			q = q[1:]
			if len(q) > 0 {
				p.queues[owner] = q
				p.owners = append(p.owners, owner)
			} else {
				delete(p.queues, owner)
			}
			w.granted = true
			p.waiting--
			p.running++
			p.active[owner]++
			close(w.ready)
			progress = true
			break
		}
		if !progress {
			return
		}
	}
}

type Stats struct {
	Running     int `json:"running"`
	Waiting     int `json:"waiting"`
	PeakWaiting int `json:"peak_waiting"`
	Concurrency int `json:"concurrency"`
	QueueLimit  int `json:"queue_limit"`
}

func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Stats{p.running, p.waiting, p.peak, p.limit, p.maxWaiting}
}
