package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllowsWithinLimit(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)

	calls := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Middleware(next)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.RemoteAddr = "192.0.2.10:12345"
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("request %d expected 200, got %d", i+1, rr.Code)
		}
	}

	if calls != 2 {
		t.Fatalf("expected 2 downstream calls, got %d", calls)
	}
}

func TestRateLimiterRejectsOverLimit(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Middleware(next)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "192.0.2.10:12345"
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("first request expected 200, got %d", rr1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "192.0.2.10:12345"
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request expected 429, got %d", rr2.Code)
	}

	if rr2.Header().Get(RateLimitLimitHeader) != "1" {
		t.Fatalf("expected rate limit header")
	}

	if rr2.Header().Get(RetryAfterHeader) == "" {
		t.Fatalf("expected retry-after header")
	}
}

func TestRateLimiterUsesForwardedFor(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Middleware(next)

	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "192.0.2.10:12345"
	req1.Header.Set("X-Forwarded-For", "203.0.113.5, 192.0.2.10")
	rr1 := httptest.NewRecorder()
	handler.ServeHTTP(rr1, req1)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "192.0.2.11:12345"
	req2.Header.Set("X-Forwarded-For", "203.0.113.5")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("expected shared forwarded client to be rate limited, got %d", rr2.Code)
	}
}
