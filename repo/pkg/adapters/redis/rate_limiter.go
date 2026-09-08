package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	goredis "github.com/redis/go-redis/v9"
)

var _ ports.RateLimiter = (*RateLimiter)(nil)

var ErrRateLimiterUnavailable = errors.New("rate limiter unavailable")

// RateLimiter is the shared, process-safe limiter used by policy enforcement.
type RateLimiter struct{ client goredis.UniversalClient }

func NewRateLimiter(client goredis.UniversalClient) *RateLimiter {
	return &RateLimiter{client: client}
}

var fixedWindowScript = goredis.NewScript(`local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]) end; return n`)
var leaseAcquireScript = goredis.NewScript(`local now=tonumber(ARGV[1]); local ttl=tonumber(ARGV[2]); local limit=tonumber(ARGV[3]); local lease=ARGV[4]; redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now); if redis.call('ZCARD',KEYS[1])>=limit then return 0 end; redis.call('ZADD',KEYS[1],now+ttl,lease); local latest=redis.call('ZRANGE',KEYS[1],-1,-1,'WITHSCORES'); if #latest==2 then redis.call('PEXPIREAT',KEYS[1],math.floor(tonumber(latest[2]))) end; return 1`)
var leaseReleaseScript = goredis.NewScript(`if redis.call('ZREM',KEYS[1],ARGV[1])==1 then if redis.call('ZCARD',KEYS[1])==0 then redis.call('DEL',KEYS[1]) end; return 1 end; return 0`)

func (l *RateLimiter) AllowFixedWindow(ctx context.Context, key string, limit int, window time.Duration, now time.Time) (bool, time.Duration, error) {
	if limit <= 0 {
		return true, 0, nil
	}
	if l == nil || l.client == nil {
		return false, 0, ErrRateLimiterUnavailable
	}
	n, err := fixedWindowScript.Run(ctx, l.client, []string{key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, 0, fmt.Errorf("fixed window: %w", err)
	}
	if n <= int64(limit) {
		return true, 0, nil
	}
	ttl, err := l.client.TTL(ctx, key).Result()
	if err != nil {
		return false, 0, fmt.Errorf("fixed window ttl: %w", err)
	}
	return false, ttl, nil
}

func (l *RateLimiter) AcquireLease(ctx context.Context, key string, limit int, ttl time.Duration, now time.Time) (string, bool, time.Duration, error) {
	if limit <= 0 {
		return "", true, 0, nil
	}
	if l == nil || l.client == nil {
		return "", false, 0, ErrRateLimiterUnavailable
	}
	if ttl <= 0 || now.IsZero() {
		return "", false, 0, ErrRateLimiterUnavailable
	}
	token := uuid.NewString()
	leaseKey := key + ":lease:" + token
	ok, err := leaseAcquireScript.Run(ctx, l.client, []string{key}, now.UnixMilli(), ttl.Milliseconds(), limit, leaseKey).Int64()
	if err != nil {
		return "", false, 0, fmt.Errorf("acquire lease: %w", err)
	}
	if ok == 1 {
		return leaseKey, true, 0, nil
	}
	remainingTTL, err := l.client.PTTL(ctx, key).Result()
	if err != nil {
		return "", false, 0, fmt.Errorf("lease ttl: %w", err)
	}
	return "", false, remainingTTL, nil
}

func (l *RateLimiter) ReleaseLease(ctx context.Context, leaseID string) error {
	if leaseID == "" {
		return nil
	}
	if l == nil || l.client == nil {
		return ErrRateLimiterUnavailable
	}
	const marker = ":lease:"
	idx := len(leaseID)
	for i := len(leaseID) - len(marker); i >= 0; i-- {
		if leaseID[i:i+len(marker)] == marker {
			idx = i
			break
		}
	}
	if idx == len(leaseID) {
		return fmt.Errorf("invalid lease id")
	}
	key := leaseID[:idx]
	if _, err := leaseReleaseScript.Run(ctx, l.client, []string{key}, leaseID).Result(); err != nil {
		return fmt.Errorf("release lease: %w", err)
	}
	return nil
}
