package service

import (
	"context"
	"time"
)

// RateLimiter provides process-safe request and in-flight concurrency limits
// for inference access policy enforcement.
type RateLimiter interface {
	AllowFixedWindow(context.Context, string, int, time.Duration, time.Time) (bool, time.Duration, error)
	AcquireLease(context.Context, string, int, time.Duration, time.Time) (string, bool, time.Duration, error)
	ReleaseLease(context.Context, string) error
}
