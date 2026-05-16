package vmm

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"net/http"
	"time"
)

// shouldRetryStatus returns true for HTTP status codes that are safe to retry.
func shouldRetryStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// isRetryableError returns true for transient network errors.
func isRetryableError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	return true
}

// backoffDelay computes the delay for the given attempt (0-based) using
// exponential backoff with full jitter.
func backoffDelay(attempt int, policy RetryPolicy) time.Duration {
	if policy.BaseDelay == 0 {
		policy.BaseDelay = 250 * time.Millisecond
	}
	if policy.Multiplier == 0 {
		policy.Multiplier = 2.0
	}
	if policy.MaxDelay == 0 {
		policy.MaxDelay = 5 * time.Second
	}

	exp := float64(policy.BaseDelay) * math.Pow(policy.Multiplier, float64(attempt))
	max := float64(policy.MaxDelay)
	if exp > max {
		exp = max
	}

	// Full jitter: random value in [0, exp)
	jittered := rand.Float64() * exp
	return time.Duration(jittered)
}