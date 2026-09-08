package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

var _ ports.RateLimiter = (*RateLimiter)(nil)

func TestRateLimiterAdapterImplementsPort(t *testing.T) {
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
