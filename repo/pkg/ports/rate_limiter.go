package ports

import (
	"context"
	"time"
)

// RateLimiter provides process-safe request and in-flight concurrency limits.
// Implementations belong in adapters so business services depend only on this port.
type RateLimiter interface {
	AllowFixedWindow(context.Context, string, int, time.Duration, time.Time) (bool, time.Duration, error)
	AcquireLease(context.Context, string, int, time.Duration, time.Time) (string, bool, time.Duration, error)
	ReleaseLease(context.Context, string) error
}
