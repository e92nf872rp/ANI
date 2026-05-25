package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/kubercloud/ani/pkg/adapters/postgres"
	"github.com/kubercloud/ani/pkg/ports"
)

// connectDB creates a pgxpool with retry logic.
// It retries every 2 seconds for up to 30 seconds before giving up.
func connectDB(databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid DATABASE_URL: %w", err)
	}

	// Connection pool tuning
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var pool *pgxpool.Pool
	for {
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			// Verify connection and DB version
			var version string
			if err2 := pool.QueryRow(ctx, "SHOW server_version").Scan(&version); err2 == nil {
				slog.Info("database connected", "version", version)
				return pool, nil
			}
			pool.Close()
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("database connection timeout after 30s: %w", err)
		case <-time.After(2 * time.Second):
			slog.Warn("database not ready, retrying...", "err", err)
		}
	}
}

func ConnectMetadataStore(ctx context.Context, databaseURL string) (ports.MetadataStore, func(), error) {
	closeStore := func() {}
	if err := ctx.Err(); err != nil {
		return nil, closeStore, err
	}
	pool, err := connectDB(databaseURL)
	if err != nil {
		return nil, closeStore, err
	}
	return postgresadapter.NewMetadataStore(pool), pool.Close, nil
}
