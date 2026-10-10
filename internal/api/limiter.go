package api

import (
	"strconv"
	"sync"
	"time"
)

func itoa(n int) string { return strconv.Itoa(n) }

// Limits (ADR 0015): a steady rate per token, and a block on a source
// address that keeps presenting bad tokens.
const (
	tokenRate  = 10.0 // requests per second, per token
	tokenBurst = 40.0
	failLimit  = 10 // bad tokens per window before the address is blocked
	failWindow = time.Minute
	pruneAbove = 1024
)

type bucket struct {
	tokens float64
	last   time.Time
}

type failures struct {
	n     int
	since time.Time
}

type limiter struct {
	now     func() time.Time
	mu      sync.Mutex
	buckets map[string]*bucket
	fails   map[string]*failures
}

func newLimiter(now func() time.Time) *limiter {
	return &limiter{now: now, buckets: map[string]*bucket{}, fails: map[string]*failures{}}
}

// allow takes one request from a token's bucket.
func (l *limiter) allow(id string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[id]
	if b == nil {
		if len(l.buckets) > pruneAbove {
			for k, v := range l.buckets {
				if now.Sub(v.last) > time.Minute {
					delete(l.buckets, k)
				}
			}
		}
		b = &bucket{tokens: tokenBurst, last: now}
		l.buckets[id] = b
	}
	b.tokens = min(tokenBurst, b.tokens+now.Sub(b.last).Seconds()*tokenRate)
	b.last = now
	if b.tokens < 1 {
		return false, time.Duration((1 - b.tokens) / tokenRate * float64(time.Second))
	}
	b.tokens--
	return true, 0
}

// fail records a bad token from ip.
func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	f := l.fails[ip]
	if f == nil || now.Sub(f.since) >= failWindow {
		if len(l.fails) > pruneAbove {
			for k, v := range l.fails {
				if now.Sub(v.since) >= failWindow {
					delete(l.fails, k)
				}
			}
		}
		f = &failures{since: now}
		l.fails[ip] = f
	}
	f.n++
}

// blocked reports whether ip has used up its bad-token allowance.
func (l *limiter) blocked(ip string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[ip]
	if f == nil {
		return false, 0
	}
	if wait := failWindow - l.now().Sub(f.since); wait > 0 && f.n >= failLimit {
		return true, wait
	}
	return false, 0
}
