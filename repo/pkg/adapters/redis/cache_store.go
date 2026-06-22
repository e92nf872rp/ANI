package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	goredis "github.com/redis/go-redis/v9"
)

type CacheStore struct {
	client *goredis.Client
}

var _ ports.CacheStore = (*CacheStore)(nil)

func NewCacheStore(client *goredis.Client) *CacheStore {
	return &CacheStore{client: client}
}

func (s *CacheStore) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := s.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cache get: %w", err)
	}
	return value, nil
}

func (s *CacheStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if err := s.client.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}

func (s *CacheStore) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	ok, err := s.client.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("cache setnx: %w", err)
	}
	return ok, nil
}

func (s *CacheStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("cache delete: %w", err)
	}
	return nil
}

func (s *CacheStore) Increment(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	pipe := s.client.TxPipeline()
	incr := pipe.Incr(ctx, key)
	if ttl > 0 {
		pipe.Expire(ctx, key, ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("cache increment: %w", err)
	}
	return incr.Val(), nil
}

func (s *CacheStore) Exists(ctx context.Context, key string) (bool, error) {
	count, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("cache exists: %w", err)
	}
	return count > 0, nil
}
