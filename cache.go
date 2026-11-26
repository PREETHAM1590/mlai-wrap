package main

// cache.go — tiny TTL + LRU cache for identical requests (no external deps).

import (
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"
)

// Defaults for the byte limits NewLRU applies: the whole cache and one entry.
// Tests may change them before creating a cache.
var (
	cacheMaxBytes = 64 << 20
	cacheMaxEntry = 1 << 20
)

type cacheEntry struct {
	key     string
	value   []byte
	expires time.Time
}

func (e *cacheEntry) size() int { return len(e.key) + len(e.value) }

type LRU struct {
	mu       sync.Mutex
	cap      int
	maxBytes int
	maxEntry int
	bytes    int
	ttl      time.Duration
	items    map[string]*list.Element
	order    *list.List
	hits     int64
	miss     int64
}

func NewLRU(capacity int, ttl time.Duration) *LRU {
	if capacity <= 0 {
		capacity = 256
	}
	return &LRU{
		cap:      capacity,
		maxBytes: cacheMaxBytes,
		maxEntry: cacheMaxEntry,
		ttl:      ttl,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

func (c *LRU) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		c.miss++
		return nil, false
	}
	e := el.Value.(*cacheEntry)
	if time.Now().After(e.expires) {
		c.order.Remove(el)
		delete(c.items, key)
		c.bytes -= e.size()
		c.miss++
		return nil, false
	}
	c.order.MoveToFront(el)
	c.hits++
	return e.value, true
}

func (c *LRU) Put(key string, value []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(value) > c.maxEntry {
		if el, ok := c.items[key]; ok { // the old answer is stale now
			c.bytes -= el.Value.(*cacheEntry).size()
			c.order.Remove(el)
			delete(c.items, key)
		}
		return
	}
	if el, ok := c.items[key]; ok {
		e := el.Value.(*cacheEntry)
		c.bytes += len(value) - len(e.value)
		e.value = value
		e.expires = time.Now().Add(c.ttl)
		c.order.MoveToFront(el)
	} else {
		e := &cacheEntry{key: key, value: value, expires: time.Now().Add(c.ttl)}
		c.items[key] = c.order.PushFront(e)
		c.bytes += e.size()
	}
	for c.order.Len() > c.cap || c.bytes > c.maxBytes {
		last := c.order.Back()
		if last == nil {
			break
		}
		e := last.Value.(*cacheEntry)
		c.order.Remove(last)
		delete(c.items, e.key)
		c.bytes -= e.size()
	}
}

func (c *LRU) Stats() (hits, miss int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.miss
}

// cacheKey hashes everything that changes the answer.
func cacheKey(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
