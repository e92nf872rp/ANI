package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	redisadapter "github.com/kubercloud/ani/pkg/adapters/redis"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/redis/go-redis/v9"
)

func connectRedis(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
	}

	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	slog.Info("redis connected")
	return rdb, nil
}

func ConnectRedisCacheStore(redisURL string) (ports.CacheStore, func() error, error) {
	client, err := connectRedis(redisURL)
	if err != nil {
		return nil, nil, err
	}
	return redisadapter.NewCacheStore(client), client.Close, nil
}
