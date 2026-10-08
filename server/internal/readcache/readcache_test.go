package readcache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestCache[V any](mut func(*Options)) *Cache[V] {
	o := Options{Capacity: 16, TTL: time.Minute, Epoch: func() uint64 { return 1 }}
	if mut != nil {
		mut(&o)
	}
	return New[V](o)
}

func TestCacheHitMiss(t *testing.T) {
	c := newTestCache[string](nil)
	if _, ok := c.Get("k"); ok {
		t.Fatal("cold key must miss")
	}
	c.Set("k", "v")
	if v, ok := c.Get("k"); !ok || v != "v" {
		t.Fatalf("hit = %q,%v", v, ok)
	}
	if c.Hits() != 1 || c.Misses() != 1 {
		t.Fatalf("stats hits=%d misses=%d", c.Hits(), c.Misses())
	}
}

func TestCacheTTLExpiry(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	c := newTestCache[string](func(o *Options) { o.TTL = 3 * time.Second; o.Now = func() time.Time { return now } })
	c.Set("k", "v")
	if _, ok := c.Get("k"); !ok {
		t.Fatal("within TTL must hit")
	}
	now = now.Add(3*time.Second + time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Fatal("past TTL must miss")
	}
}

func TestJitterOffsetBounds(t *testing.T) {
	base, spread := 10*time.Second, 4*time.Second
	for i := 0; i < 1000; i++ {
		u := float64(i%101) / 100.0
		got := base + jitterOffset(spread, u)
		if got < base-spread/2 || got > base+spread/2 {
			t.Fatalf("jitter out of bounds: %s", got)
		}
	}
}

func TestCacheEpochInvalidation(t *testing.T) {
	epoch := atomic.Uint64{}
	epoch.Store(7)
	c := newTestCache[string](func(o *Options) { o.Epoch = epoch.Load })
	c.Set("k", "v")
	if _, ok := c.Get("k"); !ok {
		t.Fatal("same epoch must hit")
	}
	epoch.Store(8)
	if _, ok := c.Get("k"); ok {
		t.Fatal("bumped epoch must invalidate")
	}
	c.Set("k", "v2")
	if v, ok := c.Get("k"); !ok || v != "v2" {
		t.Fatalf("re-set after bump = %q,%v", v, ok)
	}
}

func TestCacheLRUCapacity(t *testing.T) {
	c := newTestCache[int](func(o *Options) { o.Capacity = 2 })
	c.Set("a", 1)
	c.Set("b", 2)
	if _, ok := c.Get("a"); !ok { // touch a: b becomes LRU
		t.Fatal("a must be present")
	}
	c.Set("c", 3) // evicts b
	if _, ok := c.Get("b"); ok {
		t.Fatal("b must be LRU-evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a must survive (recently used)")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c must survive")
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d", c.Len())
	}
}

func TestGetOrLoadSingleflight(t *testing.T) {
	var loads atomic.Int64
	c := newTestCache[string](nil)
	const n = 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	vals := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, _, err := c.GetOrLoad("hot", func() (string, error) {
				loads.Add(1)
				time.Sleep(20 * time.Millisecond) // widen the race window
				return "loaded", nil
			})
			vals[i], errs[i] = v, err
		}(i)
	}
	close(start)
	wg.Wait()
	if got := loads.Load(); got != 1 {
		t.Fatalf("loader ran %d times, want exactly 1 (miss merging)", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if vals[i] != "loaded" {
			t.Fatalf("goroutine %d got %q", i, vals[i])
		}
	}
}

func TestGetOrLoadErrorNotCached(t *testing.T) {
	c := newTestCache[string](nil)
	calls := 0
	_, _, err := c.GetOrLoad("k", func() (string, error) { calls++; return "", errors.New("db down") })
	if err == nil {
		t.Fatal("loader error must surface")
	}
	if _, _, err := c.GetOrLoad("k", func() (string, error) { calls++; return "fresh", nil }); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2 (errors are never cached)", calls)
	}
	v, _ := c.Get("k")
	if v != "fresh" {
		t.Fatalf("cached value = %q", v)
	}
}

func TestDisableOneWay(t *testing.T) {
	c := newTestCache[string](nil)
	c.Set("k", "v")
	c.Disable()
	c.Disable() // idempotent
	if !c.Disabled() {
		t.Fatal("disabled state must latch")
	}
	if _, ok := c.Get("k"); ok {
		t.Fatal("disabled cache must miss")
	}
	if c.Len() != 0 {
		t.Fatal("disable must clear entries")
	}
	calls := 0
	if _, _, err := c.GetOrLoad("k", func() (string, error) { calls++; return "direct", nil }); err != nil || calls != 1 {
		t.Fatalf("disabled GetOrLoad must go straight through: calls=%d err=%v", calls, err)
	}
	c.Set("k2", "x")
	if _, ok := c.Get("k2"); ok {
		t.Fatal("disabled Set must not store")
	}
}

func TestZeroTTLNotStored(t *testing.T) {
	c := newTestCache[string](func(o *Options) { o.TTL = -time.Second })
	c.Set("k", "v")
	if c.Len() != 0 {
		t.Fatal("non-positive TTL must not store entries")
	}
}
