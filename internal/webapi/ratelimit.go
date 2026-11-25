package webapi

import (
	"sync"
	"time"
)

// ipLimiter is a tiny per-IP fixed-window limiter. It intentionally favours a
// small implementation over precision: the simulator is an internal tool and
// the goal is to stop accidental request storms, not to shape traffic.
type ipLimiter struct {
	mu     sync.Mutex
	window time.Time
	counts map[string]int
	limit  int
}

func newIPLimiter(limit int) *ipLimiter {
	if limit <= 0 {
		limit = 120
	}
	return &ipLimiter{counts: make(map[string]int), limit: limit}
}

func (l *ipLimiter) allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= time.Second {
		l.window = now
		l.counts = make(map[string]int)
	}
	if l.counts[ip] >= l.limit {
		return false
	}
	l.counts[ip]++
	return true
}
