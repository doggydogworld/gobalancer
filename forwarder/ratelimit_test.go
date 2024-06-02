package forwarder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

func TestPerClientRateLimiter(t *testing.T) {
	rl := &perClientRateLimiter{
		maxTokens:            3,
		tokenRefillPerSecond: 0,
		clientRL:             make(map[string]*rate.Limiter),
	}

	// We should receive 3 connections out of the rate limiter
	for range 3 {
		assert.NoError(t, rl.rateLimit("bob"))
	}

	assert.Error(t, rl.rateLimit("bob"))
	assert.NoError(t, rl.rateLimit("wendy"))
}
