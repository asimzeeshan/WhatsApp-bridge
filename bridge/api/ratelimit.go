package api

import (
	"math/rand/v2"
	"net/http"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter wraps a token bucket limiter with configurable jitter.
type RateLimiter struct {
	limiter  *rate.Limiter
	jitterMs int
}

func NewRateLimiter(messagesPerSecond float64, burst, jitterMs int) *RateLimiter {
	return &RateLimiter{
		limiter:  rate.NewLimiter(rate.Limit(messagesPerSecond), burst),
		jitterMs: jitterMs,
	}
}

// Middleware applies rate limiting to the wrapped handler.
// Only used on send endpoints.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.limiter.Allow() {
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED",
				"too many messages, please slow down")
			return
		}

		// Add jitter to simulate human timing
		if rl.jitterMs > 0 {
			jitter := time.Duration(rand.IntN(rl.jitterMs)) * time.Millisecond
			time.Sleep(jitter)
		}

		next.ServeHTTP(w, r)
	})
}
