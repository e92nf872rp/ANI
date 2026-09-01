package service

import (
	"context"
	"testing"
	"time"
)

type rateLimiterContractFake struct{}

func (rateLimiterContractFake) AllowFixedWindow(context.Context, string, int, time.Duration, time.Time) (bool, time.Duration, error) {
	return true, 0, nil
}

func (rateLimiterContractFake) AcquireLease(context.Context, string, int, time.Duration, time.Time) (string, bool, time.Duration, error) {
	return "", true, 0, nil
}

func (rateLimiterContractFake) ReleaseLease(context.Context, string) error { return nil }

func TestRateLimiterPortIsOwnedByAccessPolicyService(t *testing.T) {
	var limiter RateLimiter = rateLimiterContractFake{}
	if limiter == nil {
		t.Fatal("RateLimiter contract rejected a service-owned implementation")
	}
}
