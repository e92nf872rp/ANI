// Package bootstrap provides Services-layer process wiring for ANI business
// microservices (tenant-service, model-service, …).
//
// It mirrors the shape of Core `pkg/bootstrap` (Config / MustConnect / RunGRPC)
// but MUST NOT import Core pkg/ports, pkg/adapters, or pkg/bootstrap — Services
// call Core only via OpenAPI/SDK at the business layer, not via Core bootstrap.
package bootstrap

import (
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds connection and listen settings for a Services process.
// Each service's config.Load() fills this from environment variables.
type Config struct {
	DatabaseURL string
	GRPCPort    int
	HealthPort  int
	ServiceName string
}

// Deps holds initialized process dependencies after MustConnect.
type Deps struct {
	DB     *pgxpool.Pool
	Logger *slog.Logger

	ServiceName string
	HealthPort  int
}

// MustConnect initializes required dependencies. Exits the process if DB fails.
func MustConnect(cfg Config) *Deps {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	db, err := connectDB(cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}

	return &Deps{
		DB:          db,
		Logger:      logger,
		ServiceName: cfg.ServiceName,
		HealthPort:  cfg.HealthPort,
	}
}

// Close releases connections. Call with defer after MustConnect.
func (d *Deps) Close() {
	if d == nil {
		return
	}
	if d.DB != nil {
		d.DB.Close()
	}
}
