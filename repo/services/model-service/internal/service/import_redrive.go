package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kubercloud/ani/pkg/types"
	modelrepo "github.com/kubercloud/ani/services/model-service/internal/repo"
)

const (
	importRedriveScanLimit = 50
	// defaultImportRedriveInterval and defaultImportRedriveAfter bound how
	// quickly a lost delivery is noticed: a stalled import is re-queued within
	// staleAfter + interval of its creation.
	defaultImportRedriveInterval = time.Minute
	defaultImportRedriveAfter    = 2 * time.Minute
)

// ImportRedriveSweeper re-queues model imports whose outbox event was published
// but never delivered to the import worker. Without it such a task stays
// 'pending' forever: ANI_TASKS expires the undelivered message after its MaxAge
// and the worker never records an attempt, so no existing state machine can
// observe the loss.
//
// The sweeper only re-queues; the import's own lease, retry budget and terminal
// states stay with the worker, so a legitimate queue backlog is never failed by
// this compensation.
type ImportRedriveSweeper struct {
	db       *pgxpool.Pool
	interval time.Duration
	after    time.Duration
	limit    int
	logger   *slog.Logger
}

func NewImportRedriveSweeper(db *pgxpool.Pool, interval, after time.Duration, logger *slog.Logger) *ImportRedriveSweeper {
	if interval <= 0 {
		interval = defaultImportRedriveInterval
	}
	if after <= 0 {
		after = defaultImportRedriveAfter
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ImportRedriveSweeper{db: db, interval: interval, after: after, limit: importRedriveScanLimit, logger: logger}
}

// Run sweeps once at startup and then on every interval until ctx is done.
func (s *ImportRedriveSweeper) Run(ctx context.Context) {
	if s == nil || s.db == nil {
		return
	}
	s.logger.InfoContext(ctx, "model import redrive sweeper starting",
		"interval", s.interval.String(), "stale_after", s.after.String())
	timer := time.NewTicker(s.interval)
	defer timer.Stop()
	for {
		if redriven, err := s.Sweep(ctx); err != nil {
			s.logger.ErrorContext(ctx, "model import redrive sweep failed", "err", err)
		} else if redriven > 0 {
			s.logger.InfoContext(ctx, "model import redrive queued", "count", redriven)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// Sweep queues a redrive event for every never-claimed import older than the
// configured threshold and returns how many were queued.
func (s *ImportRedriveSweeper) Sweep(ctx context.Context) (int, error) {
	tenants, err := s.candidateTenants(ctx)
	if err != nil {
		return 0, err
	}
	queued := 0
	for _, tenantID := range tenants {
		tenantQueued, err := s.sweepTenant(ctx, tenantID)
		if err != nil {
			return queued, err
		}
		queued += tenantQueued
	}
	return queued, nil
}

// candidateTenants reads cross-tenant on purpose: async_tasks carries only
// permissive RLS policies, so platform scope sees the never-claimed signal.
func (s *ImportRedriveSweeper) candidateTenants(ctx context.Context) ([]uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return modelrepo.CandidateImportTenants(ctx, tx, s.after, s.limit)
}

// sweepTenant selects and re-queues one tenant's candidates in a single
// transaction; the row locks taken by SelectStalledImports are what keep
// concurrent replicas from queueing the same redrive twice.
func (s *ImportRedriveSweeper) sweepTenant(ctx context.Context, tenantID uuid.UUID) (int, error) {
	tenantCtx := types.WithTenant(ctx, &types.TenantContext{TenantID: tenantID})
	tx, err := s.db.Begin(tenantCtx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(tenantCtx) }()
	if err := types.SetDBTenant(tenantCtx, tx); err != nil {
		return 0, err
	}
	candidates, err := modelrepo.SelectStalledImports(tenantCtx, tx, s.after, s.limit)
	if err != nil {
		return 0, err
	}
	for _, candidate := range candidates {
		if err := modelrepo.InsertImportRedriveOutbox(tenantCtx, tx, candidate); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(tenantCtx); err != nil {
		return 0, err
	}
	return len(candidates), nil
}
