package render

import (
	"sync/atomic"
	"unsafe"

	"github.com/xDarkicex/memory"
)

// Cache is a high-performance, lock-free, off-heap program cache.
//
// The slot array (seq/hash/tagBits) is mmap'd via memory.MmapAnonymous
// so the Go GC never scans the hot synchronization state. Each slot is
// 24 bytes, so the read path touches at most one cache line per probe.
//
// The *Program references live in the Go-heap `programs` array
// ([]atomic.Pointer[Program]) — the same split liteLRU uses: off-heap
// for the bitmask/seqlock sync state, Go-heap for the pointers the GC
// must see to keep objects alive. A raw uintptr in off-heap memory
// would let the GC collect the Program (use-after-free), and pinning
// it (runtime.Pinner) is not lock-free — so the GC-visible reference
// array is the lock-free-correct design. Get remains a lock-free
// atomic.Pointer.Load with zero allocations.
//
// Concurrency model:
//   - Get: lock-free. Probes off-heap seq+hash; on a match, loads the
//     program via the Go-heap atomic.Pointer.
//   - Put: CAS-loop. Increments seq (locks the slot), writes hash,
//     stores the program pointer on the heap, increments seq (unlocks).
//   - Delete: same as Put with hash=0.
//
// Cache keys are (engine, name) pairs. The 64-bit hash is computed
// inline; no string concat is performed on the hot path.
type Cache struct {
	// slots is the off-heap synchronization array (seq, hash, tagBits).
	slots    []cacheSlot
	slab     []byte
	mask     uint64
	capacity int

	// programs is the Go-heap, GC-visible reference table. Indexed in
	// lockstep with slots. atomic.Pointer keeps each *Program alive.
	programs []atomic.Pointer[Program]

	hits   atomic.Int64
	misses atomic.Int64
	evicts atomic.Int64
}

// cacheSlot is 24 bytes:
//
//	seq     uint64  // 8 — sequence lock; LSB = write in progress
//	hash    uint64  // 8 — FNV-1a 64 of (engine, name)
//	tagBits uint64  // 8 — per-slot tag bitmask
//
// The *Program reference does NOT live here; it lives in the parallel
// Go-heap `programs` array so the GC keeps it alive.
type cacheSlot struct {
	seq     uint64
	hash    uint64
	tagBits uint64
}

// CacheStats is the cache-level stats.
type CacheStats struct {
	Hits     int64
	Misses   int64
	Evicts   int64
	Capacity int
}

// NewCache returns a Cache with the given capacity. Capacity is
// rounded up to the next power of two.
func NewCache(capacity int) *Cache {
	if capacity < 64 {
		capacity = 64
	}
	capacity = nextPowerOfTwo(capacity)

	slotSize := int(unsafe.Sizeof(cacheSlot{}))
	byteSize := capacity * slotSize
	slab, err := memory.MmapAnonymous(byteSize)
	if err != nil {
		slab = make([]byte, byteSize)
	}
	slots := unsafe.Slice((*cacheSlot)(unsafe.Pointer(&slab[0])), capacity)

	return &Cache{
		slots:    slots,
		slab:     slab,
		programs: make([]atomic.Pointer[Program], capacity),
		mask:     uint64(capacity - 1),
		capacity: capacity,
	}
}

// Get returns the cached program for the (engine, name) pair.
func (c *Cache) Get(engine, name string) (*Program, bool) {
	if c == nil || c.slots == nil {
		return nil, false
	}
	h := fnvPair(engine, name)
	idx := h & c.mask
	for i := uint64(0); i < uint64(c.capacity); i++ {
		s := &c.slots[(idx+i)&c.mask]
		seq1 := atomic.LoadUint64(&s.seq)
		if seq1&1 != 0 {
			continue
		}
		curHash := atomic.LoadUint64(&s.hash)
		if curHash == 0 {
			c.misses.Add(1)
			return nil, false
		}
		if curHash != h {
			continue
		}
		seq2 := atomic.LoadUint64(&s.seq)
		if seq1 != seq2 {
			continue
		}
		// Load the program from the Go-heap reference table — lock-free
		// atomic.Pointer.Load, zero alloc, GC keeps the program alive.
		pgm := c.programs[(idx+i)&c.mask].Load()
		if pgm == nil {
			c.misses.Add(1)
			return nil, false
		}
		c.hits.Add(1)
		return pgm, true
	}
	c.misses.Add(1)
	return nil, false
}

// Put inserts a program into the cache under the (engine, name) pair.
func (c *Cache) Put(engine, name string, p *Program) {
	if c == nil || p == nil || c.slots == nil {
		return
	}
	h := fnvPair(engine, name)
	idx := h & c.mask
	for i := uint64(0); i < uint64(c.capacity); i++ {
		s := &c.slots[(idx+i)&c.mask]
		for {
			seq := atomic.LoadUint64(&s.seq)
			if seq&1 != 0 {
				break
			}
			curHash := atomic.LoadUint64(&s.hash)
			if curHash != 0 && curHash != h {
				break
			}
			if !atomic.CompareAndSwapUint64(&s.seq, seq, seq+1) {
				continue
			}
			atomic.StoreUint64(&s.hash, h)
			c.programs[(idx+i)&c.mask].Store(p)
			atomic.StoreUint64(&s.seq, seq+2)
			return
		}
	}
	// Table full; evict LRU.
	c.evictLRU(h, p)
}

// evictLRU scans the slot array and evicts the slot with the oldest
// lastAccess. Approximated by the seq field's high bits.
func (c *Cache) evictLRU(newHash uint64, p *Program) {
	var oldestIdx uint64
	var oldestSeq uint64 = 1<<63 - 1
	for i := uint64(0); i < uint64(c.capacity); i++ {
		s := &c.slots[i]
		seq := atomic.LoadUint64(&s.seq)
		if seq&1 != 0 {
			continue
		}
		if seq < oldestSeq {
			oldestSeq = seq
			oldestIdx = i
		}
	}
	s := &c.slots[oldestIdx]
	for {
		seq := atomic.LoadUint64(&s.seq)
		if seq&1 != 0 {
			continue
		}
		if !atomic.CompareAndSwapUint64(&s.seq, seq, seq+1) {
			continue
		}
		atomic.StoreUint64(&s.hash, newHash)
		c.programs[oldestIdx].Store(p)
		atomic.StoreUint64(&s.seq, seq+2)
		c.evicts.Add(1)
		return
	}
}

// Delete removes the cached program for the (engine, name) pair.
func (c *Cache) Delete(engine, name string) {
	if c == nil || c.slots == nil {
		return
	}
	h := fnvPair(engine, name)
	idx := h & c.mask
	for i := uint64(0); i < uint64(c.capacity); i++ {
		s := &c.slots[(idx+i)&c.mask]
		for {
			seq := atomic.LoadUint64(&s.seq)
			if seq&1 != 0 {
				continue
			}
			curHash := atomic.LoadUint64(&s.hash)
			if curHash != h {
				break
			}
			if !atomic.CompareAndSwapUint64(&s.seq, seq, seq+1) {
				continue
			}
			atomic.StoreUint64(&s.hash, 0)
			c.programs[(idx+i)&c.mask].Store(nil)
			atomic.StoreUint64(&s.seq, seq+2)
			return
		}
	}
}

// SetTag associates a (engine, name) pair with the given tags.
// Tags are stored in the slot's tag bitmask. Up to 64 distinct tags
// per slot. Tag names are FNV-1a hashed into bit positions 0..63.
func (c *Cache) SetTag(engine, name string, tags ...string) {
	if c == nil || c.slots == nil {
		return
	}
	h := fnvPair(engine, name)
	idx := h & c.mask
	for i := uint64(0); i < uint64(c.capacity); i++ {
		s := &c.slots[(idx+i)&c.mask]
		for {
			seq := atomic.LoadUint64(&s.seq)
			if seq&1 != 0 {
				break
			}
			curHash := atomic.LoadUint64(&s.hash)
			if curHash != h {
				break
			}
			if !atomic.CompareAndSwapUint64(&s.seq, seq, seq+1) {
				continue
			}
			var mask uint64
			for _, t := range tags {
				mask |= 1 << (fnv1a64(t) & 63)
			}
			atomic.StoreUint64(&s.tagBits, atomic.LoadUint64(&s.tagBits)|mask)
			atomic.StoreUint64(&s.seq, seq+2)
			return
		}
	}
}

// InvalidateTag drops every cached entry whose tag bit matches.
func (c *Cache) InvalidateTag(tag string) int {
	if c == nil || c.slots == nil {
		return 0
	}
	bit := uint64(1) << (fnv1a64(tag) & 63)
	n := 0
	for i := uint64(0); i < uint64(c.capacity); i++ {
		s := &c.slots[i]
		for {
			seq := atomic.LoadUint64(&s.seq)
			if seq&1 != 0 {
				continue
			}
			if atomic.LoadUint64(&s.tagBits)&bit == 0 {
				break
			}
			if !atomic.CompareAndSwapUint64(&s.seq, seq, seq+1) {
				continue
			}
			atomic.StoreUint64(&s.hash, 0)
			c.programs[i].Store(nil)
			atomic.StoreUint64(&s.tagBits, 0)
			atomic.StoreUint64(&s.seq, seq+2)
			n++
			break
		}
	}
	return n
}

// Stats returns the cache-level stats.
func (c *Cache) Stats() CacheStats {
	if c == nil {
		return CacheStats{}
	}
	return CacheStats{
		Hits:     c.hits.Load(),
		Misses:   c.misses.Load(),
		Evicts:   c.evicts.Load(),
		Capacity: c.capacity,
	}
}

// Close releases the off-heap memory. Programs remain referenced by
// the Go-heap `programs` array; the cache must not be used after Close.
func (c *Cache) Close() {
	if c == nil {
		return
	}
	if len(c.slab) > 0 {
		_ = memory.Munmap(c.slab)
	}
	c.slab = nil
	c.slots = nil
}

// fnv1a64 computes the FNV-1a 64-bit hash of a string.
func fnv1a64(s string) uint64 {
	const (
		offset uint64 = 14695981039346656037
		prime  uint64 = 1099511628211
	)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// fnvPair computes a 64-bit hash of the (engine, name) pair plus an
// optional variant suffix. Two FNV-1a hashes combined via prime
// multiplication. Zero allocations.
func fnvPair(engine, name string) uint64 {
	const prime uint64 = 1099511628211
	h := fnv1a64(engine)
	h = h*prime + fnv1a64(name)
	return h
}

// fnvPairVariant is fnvPair with a variant suffix.
func fnvPairVariant(engine, name, variant string) uint64 {
	const prime uint64 = 1099511628211
	h := fnv1a64(engine)
	h = h*prime + fnv1a64(name)
	if variant != "" {
		h = h*prime + fnv1a64(variant)
	}
	return h
}

// keep referenced for variant-aware builds.
var _ = fnvPairVariant

// nextPowerOfTwo rounds n up to the next power of two.
func nextPowerOfTwo(n int) int {
	if n <= 1 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	n++
	return n
}
