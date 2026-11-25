package security

import (
	"sync"
	"time"
)

// Limiter is a keyed token bucket.
type Limiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewLimiter(perMinute, burst int) *Limiter {
	l := &Limiter{rate: float64(perMinute) / 60, burst: float64(burst), buckets: map[string]*bucket{}}
	go l.sweep()
	return l
}

func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (l *Limiter) sweep() {
	for range time.Tick(5 * time.Minute) {
		cutoff := time.Now().Add(-time.Duration(l.burst/l.rate) * time.Second)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.last.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// Backoff locks a key out for an exponentially growing time after repeated
// failures: free attempts first, then 30 s doubling up to 15 min.
type Backoff struct {
	mu   sync.Mutex
	free int
	m    map[string]*strikes
}

type strikes struct {
	fails int
	until time.Time
	last  time.Time
}

func NewBackoff(freeAttempts int) *Backoff {
	b := &Backoff{free: freeAttempts, m: map[string]*strikes{}}
	go b.sweep()
	return b
}

// Wait returns how long key is still locked out (0 when allowed).
func (b *Backoff) Wait(key string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s, ok := b.m[key]; ok {
		if d := time.Until(s.until); d > 0 {
			return d
		}
	}
	return 0
}

func (b *Backoff) Fail(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.m[key]
	if !ok {
		s = &strikes{}
		b.m[key] = s
	}
	s.fails++
	s.last = time.Now()
	if over := s.fails - b.free; over > 0 {
		d := 30 * time.Second << min(over-1, 5)
		s.until = s.last.Add(min(d, 15*time.Minute))
	}
}

func (b *Backoff) Reset(key string) {
	b.mu.Lock()
	delete(b.m, key)
	b.mu.Unlock()
}

func (b *Backoff) sweep() {
	for range time.Tick(10 * time.Minute) {
		b.mu.Lock()
		for k, s := range b.m {
			if time.Since(s.last) > time.Hour && time.Now().After(s.until) {
				delete(b.m, k)
			}
		}
		b.mu.Unlock()
	}
}
