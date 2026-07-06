package httpx

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	RateLimitLimitHeader     = "X-RateLimit-Limit"
	RateLimitRemainingHeader = "X-RateLimit-Remaining"
	RetryAfterHeader         = "Retry-After"
)

type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	clients map[string]*rateLimitBucket
	now     func() time.Time
	keyFunc func(*http.Request) string
}

type rateLimitBucket struct {
	windowStart time.Time
	count       int
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if limit <= 0 {
		limit = 60
	}
	if window <= 0 {
		window = time.Minute
	}

	rl := &RateLimiter{
		limit:   limit,
		window:  window,
		clients: make(map[string]*rateLimitBucket),
		now:     time.Now,
		keyFunc: clientKey,
	}

	return rl
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed, remaining, retryAfter := rl.allow(r)

		w.Header().Set(RateLimitLimitHeader, strconv.Itoa(rl.limit))
		w.Header().Set(RateLimitRemainingHeader, strconv.Itoa(remaining))

		if !allowed {
			w.Header().Set(RetryAfterHeader, strconv.Itoa(int(retryAfter.Seconds())))
			WriteError(w, r, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(r *http.Request) (bool, int, time.Duration) {
	key := rl.keyFunc(r)
	now := rl.now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, ok := rl.clients[key]
	if !ok || now.Sub(bucket.windowStart) >= rl.window {
		rl.clients[key] = &rateLimitBucket{
			windowStart: now,
			count:       1,
		}
		return true, rl.limit - 1, 0
	}

	if bucket.count >= rl.limit {
		retryAfter := rl.window - now.Sub(bucket.windowStart)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return false, 0, retryAfter
	}

	bucket.count++
	return true, rl.limit - bucket.count, 0
}

func clientKey(r *http.Request) string {
	forwardedFor := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if forwardedFor != "" {
		parts := strings.Split(forwardedFor, ",")
		return strings.TrimSpace(parts[0])
	}

	realIP := strings.TrimSpace(r.Header.Get("X-Real-IP"))
	if realIP != "" {
		return realIP
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}

	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}

	return "unknown"
}
