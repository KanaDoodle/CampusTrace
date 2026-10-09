package persistence

import (
	"container/list"
	"context"
	"runtime"
	"strings"
	"sync"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"github.com/KanaDoodle/CampusTrace/internal/matching"
)

// Only local display summaries are memoized. Every request still reads current
// ownership, observations, preferences, applications and model result identity
// in its SQL snapshot. This cache never authorizes an analysis or a write.
type inventoryScreenCache struct {
	mu                          sync.Mutex
	entries                     map[string]*list.Element
	lru                         *list.List
	bytes, maxBytes, maxEntries int
	once                        sync.Once
	slots                       chan struct{}
}

type inventoryScreenEntry struct {
	key   string
	value matching.LocalScreen
	bytes int
}

func cloneInventoryScreen(v matching.LocalScreen) matching.LocalScreen {
	v.Checks, v.RoleExcerpt, v.Direction.Evidence = nil, "", nil
	v.Reasons = append([]string{}, v.Reasons...)
	v.Warnings = append([]string{}, v.Warnings...)
	v.Direction.Roles = append([]string{}, v.Direction.Roles...)
	// Do not retain an entire source's backing string through a short excerpt.
	v.Role, v.Direction.Reason = strings.Clone(v.Role), strings.Clone(v.Direction.Reason)
	return v
}

func (c *inventoryScreenCache) get(key string) (matching.LocalScreen, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.lru.MoveToFront(e)
		return cloneInventoryScreen(e.Value.(inventoryScreenEntry).value), true
	}
	return matching.LocalScreen{}, false
}

func (c *inventoryScreenCache) put(key string, value matching.LocalScreen) {
	value = cloneInventoryScreen(value)
	size := 2*len(d.JSON(value)) + len(key) + 1024
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries, c.lru = map[string]*list.Element{}, list.New()
		if c.maxEntries == 0 {
			c.maxEntries = 10000
		}
		if c.maxBytes == 0 {
			c.maxBytes = 32 << 20
		}
	}
	if e := c.entries[key]; e != nil {
		c.lru.MoveToFront(e)
		return
	}
	if c.maxEntries < 1 || size > c.maxBytes {
		return
	}
	for c.lru.Len() >= c.maxEntries || c.bytes+size > c.maxBytes {
		e := c.lru.Back()
		old := e.Value.(inventoryScreenEntry)
		delete(c.entries, old.key)
		c.bytes -= old.bytes
		c.lru.Remove(e)
	}
	c.entries[key] = c.lru.PushFront(inventoryScreenEntry{key, value, size})
	c.bytes += size
}

func (s *Store) screenInventory(ctx context.Context, user string, v *MatchSnapshot, screener *matching.LocalScreener) error {
	cache := &s.inventoryScreens
	cache.once.Do(func() { cache.slots = make(chan struct{}, min(4, runtime.GOMAXPROCS(0))) })
	// Include the full profile as well as the reviewed candidate: education and
	// preference changes must also invalidate a local score, not only evidence.
	scope := d.Hash(user + "\n" + matching.LocalVersion + "\n" + v.CandidateHash + "\n" + d.JSON(v.Profile))
	var wg sync.WaitGroup
	work := make(chan int)
	for i := 0; i < min(4, len(v.Jobs)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range work {
				row := &v.Jobs[index]
				if row.RequirementsKey == "" || ctx.Err() != nil {
					continue
				}
				key := d.Hash(scope + "\n" + row.InputKey)
				local, found := cache.get(key)
				if !found {
					select {
					case cache.slots <- struct{}{}:
					case <-ctx.Done():
						continue
					}
					// A concurrent request may have completed while we waited.
					local, found = cache.get(key)
					if !found {
						local = cloneInventoryScreen(screener.ScreenSummary(row.Job, row.Text))
						cache.put(key, local)
					}
					<-cache.slots
				}
				row.Local, row.PreliminaryScore = &local, local.Score
				if row.ExcludedReason == "" {
					row.ExcludedReason = local.ExcludedReason
				}
			}
		}()
	}
send:
	for i := range v.Jobs {
		select {
		case work <- i:
		case <-ctx.Done():
			break send
		}
	}
	close(work)
	wg.Wait()
	return ctx.Err()
}
