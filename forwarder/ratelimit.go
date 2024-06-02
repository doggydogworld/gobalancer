package forwarder

import (
	"fmt"
	"sync"

	"golang.org/x/time/rate"
)

// perClientRateLimiter provides a token bucket rate limiter per client
//
// TODO: This is a rate limiter in that it drops connections that exceed the limit.
// This could be modified fairly easily to be a traffic shaper by running a goroutine
// to wait for a reservation.
type perClientRateLimiter struct {
	maxTokens int
	// Set to Math.MaxFloat64 to allow all events regardless of maxTokens
	tokenRefillPerSecond float64
	// Rate limit per client
	clientRL map[string]*rate.Limiter
	mu       sync.Mutex
}

// getRL returns a rate limiter for the given key.
// If an existing rate limiter exists for that client it is returned otherwise a new one is created and returned.
func (rl *perClientRateLimiter) getRL(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	var cl *rate.Limiter
	if val, ok := rl.clientRL[key]; !ok {
		cl = rate.NewLimiter(rate.Limit(rl.tokenRefillPerSecond), rl.maxTokens)
		rl.clientRL[key] = cl
	} else {
		cl = val
	}
	return cl
}

func (rl *perClientRateLimiter) rateLimit(key string) error {
	limiter := rl.getRL(key)
	if allowed := limiter.Allow(); !allowed {
		return fmt.Errorf("user with key '%s' has exceeded maximum rate limit %d", key, rl.maxTokens)
	}
	return nil
}
