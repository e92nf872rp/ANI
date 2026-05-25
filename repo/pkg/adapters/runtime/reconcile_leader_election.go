package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kubercloud/ani/pkg/ports"
)

type LeaderElectingWorkloadReconcileController struct {
	delegate ports.WorkloadReconcileController
	elector  ports.ReconcileLeaderElector
}

func NewLeaderElectingWorkloadReconcileController(
	delegate ports.WorkloadReconcileController,
	elector ports.ReconcileLeaderElector,
) *LeaderElectingWorkloadReconcileController {
	return &LeaderElectingWorkloadReconcileController{delegate: delegate, elector: elector}
}

func (c *LeaderElectingWorkloadReconcileController) Start(ctx context.Context) error {
	if c.delegate == nil {
		return fmt.Errorf("%w: reconcile controller delegate is required", ports.ErrNotConfigured)
	}
	if c.elector == nil {
		return fmt.Errorf("%w: reconcile leader elector is required", ports.ErrNotConfigured)
	}
	return c.elector.Run(ctx, c.delegate.Start)
}

func (c *LeaderElectingWorkloadReconcileController) ReconcileNow(ctx context.Context, target ports.ReconcileTarget) (ports.ReconcileResult, error) {
	if c.delegate == nil {
		return ports.ReconcileResult{}, fmt.Errorf("%w: reconcile controller delegate is required", ports.ErrNotConfigured)
	}
	return c.delegate.ReconcileNow(ctx, target)
}

func (c *LeaderElectingWorkloadReconcileController) Metrics() ports.ReconcileControllerMetrics {
	reader, ok := c.delegate.(ports.ReconcileControllerMetricsReader)
	if !ok || reader == nil {
		return ports.ReconcileControllerMetrics{}
	}
	return reader.Metrics()
}

type MetadataReconcileLeaderElector struct {
	store         ports.MetadataStore
	leaseName     string
	identity      string
	leaseTTL      time.Duration
	renewInterval time.Duration
	now           func() time.Time
}

type MetadataReconcileLeaderElectorConfig struct {
	LeaseName            string
	Identity             string
	LeaseTTLSeconds      int
	RenewIntervalSeconds int
}

func NewMetadataReconcileLeaderElector(store ports.MetadataStore, config MetadataReconcileLeaderElectorConfig) (*MetadataReconcileLeaderElector, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: metadata store is required for reconcile leader election", ports.ErrNotConfigured)
	}
	if config.Identity == "" {
		return nil, fmt.Errorf("%w: reconcile leader identity is required", ports.ErrNotConfigured)
	}
	leaseName := firstNonEmpty(config.LeaseName, "workload-reconcile-controller")
	ttl := time.Duration(config.LeaseTTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	renew := time.Duration(config.RenewIntervalSeconds) * time.Second
	if renew <= 0 || renew >= ttl {
		renew = ttl / 3
	}
	return &MetadataReconcileLeaderElector{
		store:         store,
		leaseName:     leaseName,
		identity:      config.Identity,
		leaseTTL:      ttl,
		renewInterval: renew,
		now:           time.Now,
	}, nil
}

func (e *MetadataReconcileLeaderElector) Run(ctx context.Context, run func(context.Context) error) error {
	if run == nil {
		return fmt.Errorf("%w: reconcile leader run function is required", ports.ErrNotConfigured)
	}
	for {
		acquired, err := e.acquire(ctx)
		if err != nil {
			return err
		}
		if acquired {
			err = e.runAsLeader(ctx, run)
			if err != nil || ctx.Err() != nil {
				return err
			}
		}
		timer := time.NewTimer(e.renewInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (e *MetadataReconcileLeaderElector) runAsLeader(ctx context.Context, run func(context.Context) error) error {
	leaderCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- run(leaderCtx)
	}()

	ticker := time.NewTicker(e.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = e.release(context.Background())
			return nil
		case err := <-errCh:
			_ = e.release(context.Background())
			return err
		case <-ticker.C:
			acquired, err := e.acquire(ctx)
			if err != nil {
				return err
			}
			if !acquired {
				cancel()
				select {
				case err := <-errCh:
					return err
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

func (e *MetadataReconcileLeaderElector) acquire(ctx context.Context) (bool, error) {
	acquired := false
	err := e.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		now := e.now().UTC()
		leaseUntil := now.Add(e.leaseTTL)
		return tx.QueryRow(ctx, `
INSERT INTO control_plane_leases (lease_name, holder_id, lease_until, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (lease_name) DO UPDATE
SET holder_id = EXCLUDED.holder_id,
    lease_until = EXCLUDED.lease_until,
    updated_at = EXCLUDED.updated_at
WHERE control_plane_leases.lease_until <= EXCLUDED.updated_at
   OR control_plane_leases.holder_id = EXCLUDED.holder_id
RETURNING true
`, e.leaseName, e.identity, leaseUntil, now).Scan(&acquired)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return acquired, err
}

func (e *MetadataReconcileLeaderElector) release(ctx context.Context) error {
	return e.store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		now := e.now().UTC()
		_, err := tx.Exec(ctx, `
UPDATE control_plane_leases
SET lease_until = $3,
    updated_at = $3
WHERE lease_name = $1
  AND holder_id = $2
`, e.leaseName, e.identity, now)
		return err
	})
}

var _ ports.WorkloadReconcileController = (*LeaderElectingWorkloadReconcileController)(nil)
var _ ports.ReconcileControllerMetricsReader = (*LeaderElectingWorkloadReconcileController)(nil)
var _ ports.ReconcileLeaderElector = (*MetadataReconcileLeaderElector)(nil)
