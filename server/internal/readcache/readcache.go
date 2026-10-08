// Package readcache is the bounded in-process read cache behind the
// HUI-2981 T1 option-C decision: pure display reads only (public short-code
// pages and the dashboard aggregation layer). It is deliberately boring:
//
//   - TTL with jitter (anti-thundering-herd): per-entry ttl = TTL ± Jitter/2,
//     so the allowed staleness upper bound is TTL + Jitter/2 (frozen in the
//     comparison report);
//   - structural-epoch validation: an entry is valid only while the epoch it
//     was loaded under equals the current epoch, which gives admin edits a
//     deterministic 0-stale-request invalidation boundary;
//   - singleflight miss merging: concurrent misses on one key trigger exactly
//     one loader call (anti-stampede);
//   - bounded capacity with LRU eviction (memory cannot grow without bound);
//   - one-way Disable() kill switch: clears everything and every later read
//     goes straight through — recovery is a restart or a flag-off redeploy;
//   - errors are NEVER cached: a failed loader surfaces its error verbatim,
//     so a cache can never turn a DB failure into an empty page or 0 metrics.
//
// It has no dependencies outside the standard library.
package readcache

import (
	"container/list"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures one cache instance. All fields are read-only after New.
type Options struct {
	// Capacity is the maximum number of entries; overflow evicts the least
	// recently used entry. Values <= 1 are clamped to 1.
	Capacity int
	// TTL is the base per-entry time to live.
	TTL time.Duration
	// Jitter is the full spread of the per-entry TTL: ttl = TTL ± Jitter/2.
	// Zero disables jitter. Negative TTL after jitter → entry not stored.
	Jitter time.Duration
	// Epoch returns the current structural generation. A cached entry is a
	// hit only while entry.epoch == Epoch(). Nil means no epoch validation.
	Epoch func() uint64
	// Now is the clock (injectable for tests). Nil = time.Now.
	Now func() time.Time
}

// jitterOffset maps u ∈ [0,1) onto ±spread/2.
func jitterOffset(spread time.Duration, u float64) time.Duration {
	return time.Duration((u*2 - 1) * float64(spread) / 2)
}

type entry[V any] struct {
	key     string
	value   V
	epoch   uint64
	expires time.Time
}

// Cache is a bounded TTL cache with epoch validation, singleflight and a
// one-way kill switch. The zero value is not usable; use New.
type Cache[V any] struct {
	o        Options
	mu       sync.Mutex
	items    map[string]*list.Element // key -> *entry
	lru      *list.List               // front = most recently used
	flights  singleflight[V]
	disabled atomic.Bool
	hits     atomic.Int64
	misses   atomic.Int64
	loads    atomic.Int64
}

// New builds a cache.
func New[V any](o Options) *Cache[V] {
	if o.Capacity <= 1 {
		o.Capacity = 1
	}
	return &Cache[V]{o: o, items: map[string]*list.Element{}, lru: list.New()}
}

func (c *Cache[V]) now() time.Time {
	if c.o.Now == nil {
		return time.Now()
	}
	return c.o.Now()
}

func (c *Cache[V]) epoch() uint64 {
	if c.o.Epoch == nil {
		return 0
	}
	return c.o.Epoch()
}

// Get returns the cached value when it exists, is unexpired, and carries the
// current structural epoch. Everything else is a miss (and the stale entry
// is dropped on sight).
func (c *Cache[V]) Get(key string) (V, bool) {
	var zero V
	if c.disabled.Load() {
		c.misses.Add(1)
		return zero, false
	}
	now := c.now()
	cur := c.epoch()
	c.mu.Lock()
	el, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		c.misses.Add(1)
		return zero, false
	}
	e := el.Value.(*entry[V])
	if now.After(e.expires) || (c.o.Epoch != nil && e.epoch != cur) {
		c.lru.Remove(el)
		delete(c.items, key)
		c.mu.Unlock()
		c.misses.Add(1)
		return zero, false
	}
	c.lru.MoveToFront(el)
	c.mu.Unlock()
	c.hits.Add(1)
	return e.value, true
}

// Set stores a value under the current epoch with a jittered TTL. No-op when
// disabled or when the jittered TTL is non-positive.
func (c *Cache[V]) Set(key string, value V) {
	if c.disabled.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.o.TTL
	if c.o.Jitter > 0 {
		ttl += jitterOffset(c.o.Jitter, rand.Float64())
	}
	if ttl <= 0 {
		return
	}
	e := &entry[V]{key: key, value: value, epoch: c.epoch(), expires: c.now().Add(ttl)}
	if el, ok := c.items[key]; ok {
		el.Value = e
		c.lru.MoveToFront(el)
	} else {
		c.items[key] = c.lru.PushFront(e)
	}
	for len(c.items) > c.o.Capacity {
		oldest := c.lru.Back()
		if oldest == nil {
			break
		}
		delete(c.items, oldest.Value.(*entry[V]).key)
		c.lru.Remove(oldest)
	}
}

// GetOrLoad is the read path: cache hit → value; miss → singleflight merge →
// loader → store → value. The bool reports a true cache hit for this caller.
// Loader errors pass through uncached, byte for byte.
func (c *Cache[V]) GetOrLoad(key string, load func() (V, error)) (V, bool, error) {
	if v, ok := c.Get(key); ok {
		return v, true, nil
	}
	v, err := c.flights.do(key, func() (V, error) {
		// double-check: a concurrent flight may have filled the entry while
		// this caller waited for the flight slot.
		if v, ok := c.Get(key); ok {
			return v, nil
		}
		c.loads.Add(1)
		v, err := load()
		if err != nil {
			return v, err
		}
		c.Set(key, v)
		return v, nil
	})
	return v, false, err
}

// Disable is the one-way kill switch (运维一键禁用): it latches the disabled
// state, clears every entry, and from then on Get always misses, Set never
// stores, and GetOrLoad calls the loader directly. There is no un-disable —
// recovery is a process restart with the feature flag off.
func (c *Cache[V]) Disable() {
	if !c.disabled.CompareAndSwap(false, true) {
		return
	}
	c.mu.Lock()
	c.items = map[string]*list.Element{}
	c.lru.Init()
	c.mu.Unlock()
}

// Disabled reports whether the kill switch has latched.
func (c *Cache[V]) Disabled() bool { return c.disabled.Load() }

// Len is the current entry count (capacity-bounded).
func (c *Cache[V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Hits / Misses / Loads are the counters behind the comparison report's
// hit-rate evidence: requests = hits+misses, backfills = loads.
func (c *Cache[V]) Hits() int64   { return c.hits.Load() }
func (c *Cache[V]) Misses() int64 { return c.misses.Load() }
func (c *Cache[V]) Loads() int64  { return c.loads.Load() }

// ---- singleflight ------------------------------------------------------------

type flightCall[V any] struct {
	wg    sync.WaitGroup
	value V
	err   error
}

// singleflight merges concurrent calls for one key into a single fn run.
type singleflight[V any] struct {
	mu sync.Mutex
	m  map[string]*flightCall[V]
}

func (sf *singleflight[V]) do(key string, fn func() (V, error)) (V, error) {
	sf.mu.Lock()
	if sf.m == nil {
		sf.m = map[string]*flightCall[V]{}
	}
	if f, ok := sf.m[key]; ok {
		sf.mu.Unlock()
		f.wg.Wait()
		return f.value, f.err
	}
	f := new(flightCall[V])
	f.wg.Add(1)
	sf.m[key] = f
	sf.mu.Unlock()

	f.value, f.err = fn()
	f.wg.Done()

	sf.mu.Lock()
	delete(sf.m, key)
	sf.mu.Unlock()
	return f.value, f.err
}
