package matching

import (
	"container/list"
	"sync"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
)

// Only redacted job parsing is shared. Candidate evidence and results are
// rebuilt per snapshot, so profile changes never reuse another user's match.
// Entries are immutable inside this package and bounded by size and count.
type localCacheEntry struct {
	key    string
	parsed localParsed
	bytes  int
}
type localParseCache struct {
	mu                          sync.Mutex
	entries                     map[string]*list.Element
	lru                         *list.List
	bytes, maxBytes, maxEntries int
}

func newLocalParseCache(entries, bytes int) *localParseCache {
	return &localParseCache{entries: map[string]*list.Element{}, lru: list.New(), maxBytes: bytes, maxEntries: entries}
}

var localJobCache = newLocalParseCache(10000, 32<<20)

func (c *localParseCache) get(text string) localParsed {
	key := d.Hash(LocalVersion + "\n" + text)
	c.mu.Lock()
	if e := c.entries[key]; e != nil {
		c.lru.MoveToFront(e)
		p := e.Value.(localCacheEntry).parsed
		c.mu.Unlock()
		return p
	}
	c.mu.Unlock()
	p := parseLocal(text)
	// Conservative accounting includes string/slice headers and LRU overhead,
	// rather than treating the serialized content as its full in-memory cost.
	size := 2*len(d.JSON(p)) + len(key) + 4096
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		return e.Value.(localCacheEntry).parsed
	}
	if size > c.maxBytes || c.maxEntries < 1 {
		return p
	}
	for c.lru.Len() >= c.maxEntries || c.bytes+size > c.maxBytes {
		e := c.lru.Back()
		old := e.Value.(localCacheEntry)
		delete(c.entries, old.key)
		c.bytes -= old.bytes
		c.lru.Remove(e)
	}
	c.entries[key] = c.lru.PushFront(localCacheEntry{key, p, size})
	c.bytes += size
	return p
}
