// Package ratelimit counts requests in Redis, so a limit holds across all
// replicas of the service.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis_rate/v10"
	goredis "github.com/redis/go-redis/v9"
)

// Limit allows Requests per Period, with bursts of up to Burst.
type Limit struct {
	Requests int
	Period   time.Duration
	Burst    int
}

type Decision struct {
	Allowed bool
	// RetryAfter is how long to wait before the next request is allowed.
	// It is only set when Allowed is false.
	RetryAfter time.Duration
}

// Limiter implements a leaky bucket (GCRA): requests are spread evenly over
// the period instead of being allowed all at once at the start of a window.
type Limiter struct {
	limiter *redis_rate.Limiter
}

func New(client *goredis.Client) *Limiter {
	return &Limiter{limiter: redis_rate.NewLimiter(client)}
}

// Allow counts one request against key.
func (l *Limiter) Allow(ctx context.Context, key string, limit Limit) (Decision, error) {
	burst := limit.Burst
	if burst <= 0 {
		burst = limit.Requests
	}
	res, err := l.limiter.Allow(ctx, key, redis_rate.Limit{
		Rate:   limit.Requests,
		Burst:  burst,
		Period: limit.Period,
	})
	if err != nil {
		return Decision{}, fmt.Errorf("rate limit %q: %w", key, err)
	}
	if res.Allowed > 0 {
		return Decision{Allowed: true}, nil
	}
	return Decision{RetryAfter: res.RetryAfter}, nil
}
