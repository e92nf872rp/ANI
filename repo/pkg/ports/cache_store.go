package ports

import (
	"context"
	"time"
)

type CacheStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	Delete(ctx context.Context, key string) error
	Increment(ctx context.Context, key string, ttl time.Duration) (int64, error)
	Exists(ctx context.Context, key string) (bool, error)
}
