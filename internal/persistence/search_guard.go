package persistence

import (
	"context"
	"errors"
	"sync"
	"time"

	a "github.com/KanaDoodle/CampusTrace/internal/searchapi"
)

var ErrSearchBusy = errors.New("search read budget or capacity exhausted")

type searchReads struct {
	once            sync.Once
	slots, fallback chan struct{}
}

func (g *searchReads) init() {
	g.once.Do(func() { g.slots = make(chan struct{}, 8); g.fallback = make(chan struct{}, 2) })
}
func (g *searchReads) enter(fallback bool) bool {
	g.init()
	slots := g.slots
	if fallback {
		slots = g.fallback
	}
	select {
	case slots <- struct{}{}:
		return true
	default:
		return false
	}
}
func (g *searchReads) leave(fallback bool) {
	if fallback {
		<-g.fallback
	} else {
		<-g.slots
	}
}
func (s *Store) SearchReadStats() (active, limit, fallback, fallbackLimit int) {
	s.searchReads.init()
	return len(s.searchReads.slots), cap(s.searchReads.slots), len(s.searchReads.fallback), cap(s.searchReads.fallback)
}
func (s *Store) guardedBasicSearch(ctx context.Context, user string, q a.Query) (JobSearch, error) {
	if e := ctx.Err(); e != nil {
		return JobSearch{}, e
	}
	if !s.searchReads.enter(true) {
		if s.Metrics != nil {
			s.Metrics.Add("search_fallback_rejected_total", 1)
		}
		return JobSearch{}, ErrSearchBusy
	}
	defer s.searchReads.leave(true)
	child, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	started := time.Now()
	out, e := s.basicSearch(child, user, q)
	if s.Metrics != nil {
		s.Metrics.Observe("search_fallback_seconds", time.Since(started).Seconds())
	}
	if e != nil {
		if ctx.Err() != nil {
			return JobSearch{}, ctx.Err()
		}
		if errors.Is(e, context.DeadlineExceeded) || child.Err() != nil {
			if s.Metrics != nil {
				s.Metrics.Add("search_fallback_timeout_total", 1)
			}
			return JobSearch{}, ErrSearchBusy
		}
		return JobSearch{}, errors.Join(ErrBackendUnavailable, e)
	}
	return out, nil
}
func (s *Store) recordSearchFallback(reason string) {
	if s.Metrics != nil {
		s.Metrics.Add("search_basic_total", 1)
		s.Metrics.Add("search_fallback_"+reason+"_total", 1)
	}
}
