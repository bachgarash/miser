package tracker

import (
	"sort"
	"sync"
	"time"
)

type Request struct {
	ID           int
	Timestamp    time.Time
	Model        string
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
	Cost         float64
	Latency      time.Duration
	StatusCode   int
	Error        string
}

type ModelStats struct {
	Model        string
	Requests     int
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
	TotalCost    float64
}

type Summary struct {
	TotalCost     float64
	TotalRequests int
	TotalInput    int
	TotalOutput   int
	TotalCacheR   int
	TotalCacheW   int
}

type Tracker struct {
	mu       sync.RWMutex
	requests []Request
	nextID   int

	// OnRecord is called (outside the lock) after every successful Record.
	// Useful for headless logging. May be nil.
	OnRecord func(Request)
}

func New() *Tracker {
	return &Tracker{}
}

func (t *Tracker) Record(r Request) {
	t.mu.Lock()
	t.nextID++
	r.ID = t.nextID
	t.requests = append(t.requests, r)
	cb := t.OnRecord
	t.mu.Unlock()

	if cb != nil {
		cb(r)
	}
}

func (t *Tracker) GetRequests() []Request {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Request, len(t.requests))
	copy(out, t.requests)
	return out
}

// GetRecentRequests returns the last n requests, newest first.
func (t *Tracker) GetRecentRequests(n int) []Request {
	t.mu.RLock()
	defer t.mu.RUnlock()

	total := len(t.requests)
	if n > total {
		n = total
	}
	out := make([]Request, n)
	for i := 0; i < n; i++ {
		out[i] = t.requests[total-1-i]
	}
	return out
}

func (t *Tracker) GetModelStats() []ModelStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	byModel := make(map[string]*ModelStats)
	for _, r := range t.requests {
		s, ok := byModel[r.Model]
		if !ok {
			s = &ModelStats{Model: r.Model}
			byModel[r.Model] = s
		}
		s.Requests++
		s.InputTokens += r.InputTokens
		s.OutputTokens += r.OutputTokens
		s.CacheRead += r.CacheRead
		s.CacheWrite += r.CacheWrite
		s.TotalCost += r.Cost
	}

	stats := make([]ModelStats, 0, len(byModel))
	for _, s := range byModel {
		stats = append(stats, *s)
	}
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].TotalCost > stats[j].TotalCost
	})
	return stats
}

func (t *Tracker) GetSummary() Summary {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var s Summary
	s.TotalRequests = len(t.requests)
	for _, r := range t.requests {
		s.TotalCost += r.Cost
		s.TotalInput += r.InputTokens
		s.TotalOutput += r.OutputTokens
		s.TotalCacheR += r.CacheRead
		s.TotalCacheW += r.CacheWrite
	}
	return s
}

func (t *Tracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requests = nil
	t.nextID = 0
}
