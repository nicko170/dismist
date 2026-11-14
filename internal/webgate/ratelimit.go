package webgate

import (
	"math"
	"net/netip"
	"sync"
	"time"
)

// Login attempts are limited per client (design §4.9: 10 a minute per
// remote), as a token bucket: a burst of loginBurst, refilled at
// loginBurst per loginWindow.
const (
	loginBurst  = 10
	loginWindow = time.Minute
	// loginLimiterSweep is the number of tracked clients above which full
	// (idle) buckets are dropped.
	loginLimiterSweep = 1024
)

type loginBucket struct {
	tokens float64
	at     time.Time
}

// loginLimiter is a per-client token bucket. IPv6 clients are keyed by
// their /64, which one host usually controls whole.
type loginLimiter struct {
	mu      sync.Mutex
	buckets map[netip.Prefix]*loginBucket
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{buckets: map[netip.Prefix]*loginBucket{}}
}

func limiterKey(addr netip.Addr) netip.Prefix {
	if !addr.IsValid() {
		return netip.Prefix{}
	}
	if addr.Is6() {
		prefix, _ := addr.Prefix(64)
		return prefix
	}
	return netip.PrefixFrom(addr, addr.BitLen())
}

// refill returns the bucket's tokens at now.
func (b *loginBucket) refill(now time.Time) float64 {
	elapsed := now.Sub(b.at)
	if elapsed < 0 {
		elapsed = 0
	}
	return math.Min(loginBurst, b.tokens+float64(elapsed)/float64(loginWindow)*loginBurst)
}

// allow takes one attempt for addr at now. When none is left it returns
// false and how long until the next one is.
func (l *loginLimiter) allow(addr netip.Addr, now time.Time) (bool, time.Duration) {
	key := limiterKey(addr)
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) >= loginLimiterSweep {
		for other, bucket := range l.buckets {
			if bucket.refill(now) >= loginBurst {
				delete(l.buckets, other)
			}
		}
	}
	bucket, ok := l.buckets[key]
	if !ok {
		bucket = &loginBucket{tokens: loginBurst, at: now}
		l.buckets[key] = bucket
	}
	bucket.tokens, bucket.at = bucket.refill(now), now
	if bucket.tokens < 1 {
		wait := time.Duration((1 - bucket.tokens) / loginBurst * float64(loginWindow))
		return false, wait
	}
	bucket.tokens--
	return true, 0
}
