package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestRateLimiterAdapterImplementsServicePort(t *testing.T) {
	limiter := NewRateLimiter(nil)
	if limiter == nil {
		t.Fatal("NewRateLimiter(nil) returned nil adapter")
	}
}

func TestRateLimiterNilClientFailsClosedForPositiveLimits(t *testing.T) {
	limiter := NewRateLimiter(nil)
	ctx := context.Background()

	allowed, _, err := limiter.AllowFixedWindow(ctx, "tenant/service/key/policy/qps", 1, time.Second, time.Now())
	if allowed || !errors.Is(err, ErrRateLimiterUnavailable) {
		t.Fatalf("positive fixed window = (%v, %v), want deny/unavailable", allowed, err)
	}
	leaseID, allowed, _, err := limiter.AcquireLease(ctx, "tenant/service/key/policy/concurrency", 1, time.Second, time.Now())
	if leaseID != "" || allowed || !errors.Is(err, ErrRateLimiterUnavailable) {
		t.Fatalf("positive lease = (%q, %v, %v), want deny/unavailable", leaseID, allowed, err)
	}
	if err := limiter.ReleaseLease(ctx, "tenant/service/key/policy/concurrency:lease:token"); !errors.Is(err, ErrRateLimiterUnavailable) {
		t.Fatalf("release non-empty lease error = %v, want unavailable", err)
	}

	if allowed, _, err := limiter.AllowFixedWindow(ctx, "unused", 0, time.Second, time.Now()); !allowed || err != nil {
		t.Fatalf("zero fixed window = (%v, %v), want allow/no error", allowed, err)
	}
	if _, allowed, _, err := limiter.AcquireLease(ctx, "unused", 0, time.Second, time.Now()); !allowed || err != nil {
		t.Fatalf("zero lease = (%v, %v), want allow/no error", allowed, err)
	}
	if err := limiter.ReleaseLease(ctx, ""); err != nil {
		t.Fatalf("empty lease release = %v, want nil", err)
	}
}

func TestRateLimiterPreservesAtomicLuaContracts(t *testing.T) {
	tests := []struct {
		name string
		got  *goredis.Script
		want *goredis.Script
	}{
		{
			name: "fixed window",
			got:  fixedWindowScript,
			want: goredis.NewScript(`local n=redis.call('INCR',KEYS[1]); if n==1 then redis.call('PEXPIRE',KEYS[1],ARGV[1]) end; return n`),
		},
		{
			name: "lease acquire",
			got:  leaseAcquireScript,
			want: goredis.NewScript(`local now=tonumber(ARGV[1]); local ttl=tonumber(ARGV[2]); local limit=tonumber(ARGV[3]); local lease=ARGV[4]; redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',now); if redis.call('ZCARD',KEYS[1])>=limit then return 0 end; redis.call('ZADD',KEYS[1],now+ttl,lease); local latest=redis.call('ZRANGE',KEYS[1],-1,-1,'WITHSCORES'); if #latest==2 then redis.call('PEXPIREAT',KEYS[1],math.floor(tonumber(latest[2]))) end; return 1`),
		},
		{
			name: "lease release",
			got:  leaseReleaseScript,
			want: goredis.NewScript(`if redis.call('ZREM',KEYS[1],ARGV[1])==1 then if redis.call('ZCARD',KEYS[1])==0 then redis.call('DEL',KEYS[1]) end; return 1 end; return 0`),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got.Hash() != test.want.Hash() {
				t.Fatalf("Lua script hash = %s, want %s", test.got.Hash(), test.want.Hash())
			}
		})
	}
}

func TestLeaseKeyScopesTokenUnderCounter(t *testing.T) {
	if got, want := leaseKey("tenant/service/key/policy/concurrency", "token"), "tenant/service/key/policy/concurrency:lease:token"; got != want {
		t.Fatalf("lease key = %q, want %q", got, want)
	}
}

func TestConcurrencyLeaseRunsAgainstRedisAndReleasesAtomically(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewRateLimiter(client)
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	server.SetTime(now)

	leaseID, allowed, _, err := limiter.AcquireLease(ctx, "tenant/service/key/policy/concurrency", 1, 2*time.Second, now)
	if err != nil || !allowed || leaseID == "" {
		t.Fatalf("acquire = (%q, %v, %v), want non-empty/allowed/nil", leaseID, allowed, err)
	}
	if _, allowed, _, err := limiter.AcquireLease(ctx, "tenant/service/key/policy/concurrency", 1, 2*time.Second, now); err != nil || allowed {
		t.Fatalf("second acquire = (%v, %v), want limited/nil", allowed, err)
	}
	if err := limiter.ReleaseLease(ctx, leaseID); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, allowed, _, err := limiter.AcquireLease(ctx, "tenant/service/key/policy/concurrency", 1, 2*time.Second, now); err != nil || !allowed {
		t.Fatalf("acquire after release = (%v, %v), want allowed/nil", allowed, err)
	}
}

func TestConcurrencyLeaseDoesNotForgetStaggeredLiveLeases(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewRateLimiter(client)
	ctx := context.Background()
	key := "tenant/service/key/policy/concurrency"
	now := time.Unix(1_700_000_000, 0).UTC()
	server.SetTime(now)

	if _, allowed, _, err := limiter.AcquireLease(ctx, key, 2, 2*time.Second, now); err != nil || !allowed {
		t.Fatalf("first acquire = (%v, %v), want allowed/nil", allowed, err)
	}
	server.FastForward(1500 * time.Millisecond)
	now = now.Add(1500 * time.Millisecond)
	server.SetTime(now)
	if _, allowed, _, err := limiter.AcquireLease(ctx, key, 2, 2*time.Second, now); err != nil || !allowed {
		t.Fatalf("second acquire = (%v, %v), want allowed/nil", allowed, err)
	}

	server.FastForward(600 * time.Millisecond)
	now = now.Add(600 * time.Millisecond)
	server.SetTime(now)
	if _, allowed, _, err := limiter.AcquireLease(ctx, key, 2, 2*time.Second, now); err != nil || !allowed {
		t.Fatalf("third acquire after first expiry = (%v, %v), want allowed/nil", allowed, err)
	}
	if _, allowed, _, err := limiter.AcquireLease(ctx, key, 2, 2*time.Second, now); err != nil || allowed {
		t.Fatalf("fourth acquire with two live leases = (%v, %v), want limited/nil", allowed, err)
	}
}

func TestConcurrencyLeaseDoesNotShortenAnExistingLongerLease(t *testing.T) {
	server := miniredis.RunT(t)
	client := goredis.NewClient(&goredis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	limiter := NewRateLimiter(client)
	ctx := context.Background()
	key := "tenant/service/key/policy/concurrency"
	now := time.Unix(1_700_000_000, 0).UTC()
	server.SetTime(now)

	if _, allowed, _, err := limiter.AcquireLease(ctx, key, 2, 5*time.Second, now); err != nil || !allowed {
		t.Fatalf("long lease acquire = (%v, %v)", allowed, err)
	}
	server.FastForward(time.Second)
	now = now.Add(time.Second)
	server.SetTime(now)
	if _, allowed, _, err := limiter.AcquireLease(ctx, key, 2, time.Second, now); err != nil || !allowed {
		t.Fatalf("short lease acquire = (%v, %v)", allowed, err)
	}
	server.FastForward(1100 * time.Millisecond)
	now = now.Add(1100 * time.Millisecond)
	server.SetTime(now)
	if _, allowed, _, err := limiter.AcquireLease(ctx, key, 1, time.Second, now); err != nil || allowed {
		t.Fatalf("acquire while original long lease lives = (%v, %v), want limited/nil", allowed, err)
	}
}
