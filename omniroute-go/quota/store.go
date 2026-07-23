package quota

import (
	"sync"
	"time"
)

// store.go — in-memory sliding-window quota store.
//
// Mirrors the QuotaStore interface (src/lib/quota/types.ts) with a
// dependency-free, mutex-guarded implementation suitable for a single-node
// admission proxy. Consumption is bucketed per (apiKey, dimension, window)
// with a rolling window derived from WINDOW_MS.

type bucket struct {
	counts   []float64 // per-slot consumption
	slotMs   int64
	nSlots   int
	lastSlot int64
}

// Store is a concurrency-safe in-memory quota store.
type Store struct {
	mu   sync.RWMutex
	data map[string]*bucket // apiKey+"\x00"+dimKey -> bucket
	now  func() time.Time
}

// NewStore returns an empty store using wall-clock time.
func NewStore() *Store {
	return &Store{data: make(map[string]*bucket), now: time.Now}
}

func (s *Store) key(apiKey string, dim DimensionKey) string {
	return apiKey + "\x00" + dim.String()
}

// advance rolls the bucket forward to the current slot, decaying old slots.
func (b *bucket) advance(nowSlot int64) {
	if b.lastSlot == 0 {
		b.lastSlot = nowSlot
		return
	}
	elapsed := nowSlot - b.lastSlot
	if elapsed <= 0 {
		return
	}
	if int(elapsed) >= b.nSlots {
		for i := range b.counts {
			b.counts[i] = 0
		}
	} else {
		for i := int64(0); i < elapsed; i++ {
			idx := int((b.lastSlot + i + 1) % int64(b.nSlots))
			b.counts[idx] = 0
		}
	}
	b.lastSlot = nowSlot
}

func (b *bucket) sum() float64 {
	var t float64
	for _, c := range b.counts {
		t += c
	}
	return t
}

func (s *Store) getBucket(apiKey string, dim DimensionKey, create bool) *bucket {
	k := s.key(apiKey, dim)
	b := s.data[k]
	if b == nil && create {
		winMs := WindowMS(dim.Window)
		if winMs <= 0 {
			winMs = WindowMS(WindowHourly)
		}
		// 60 slots per window for smooth rolling.
		const nSlots = 60
		b = &bucket{counts: make([]float64, nSlots), slotMs: winMs / nSlots, nSlots: nSlots}
		s.data[k] = b
	}
	return b
}

func (s *Store) currentSlot() int64 {
	return s.now().UnixMilli()
}

// Consume increments consumption for (apiKey, dim) by cost. Returns new total.
func (s *Store) Consume(apiKey string, dim DimensionKey, cost float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.getBucket(apiKey, dim, true)
	nowMs := s.currentSlot()
	slot := nowMs / b.slotMs
	b.advance(slot)
	idx := int(slot % int64(b.nSlots))
	b.counts[idx] += cost
	return b.sum()
}

// Peek returns current consumption for (apiKey, dim) without mutating.
func (s *Store) Peek(apiKey string, dim DimensionKey) float64 {
	s.mu.RLock()
	b := s.getBucket(apiKey, dim, false)
	s.mu.RUnlock()
	if b == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.currentSlot() / b.slotMs
	b.advance(slot)
	return b.sum()
}

// PeekBatch returns consumption for many dims at once under a single lock
// acquisition — the batched primitive the concurrent Enforce path relies on.
func (s *Store) PeekBatch(apiKey string, dims []DimensionKey) map[string]float64 {
	out := make(map[string]float64, len(dims))
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.currentSlot()
	for _, d := range dims {
		b := s.getBucket(apiKey, d, false)
		if b == nil {
			out[d.String()] = 0
			continue
		}
		b.advance(slot / b.slotMs)
		out[d.String()] = b.sum()
	}
	return out
}

// PoolConsumedTotal sums consumption across all apiKeys for one dimension.
func (s *Store) PoolConsumedTotal(poolID string, dim DimensionKey) float64 {
	suffix := "\x00" + dim.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	slot := s.currentSlot()
	var total float64
	for k, b := range s.data {
		if len(k) > len(suffix) && k[len(k)-len(suffix):] == suffix &&
			k[:len(poolID)] == poolID {
			b.advance(slot / b.slotMs)
			total += b.sum()
		}
	}
	return total
}

// Clear removes a bucket.
func (s *Store) Clear(apiKey string, dim DimensionKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, s.key(apiKey, dim))
}
