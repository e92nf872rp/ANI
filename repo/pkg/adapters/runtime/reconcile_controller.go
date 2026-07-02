package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
	"github.com/google/uuid"
)

type LocalWorkloadReconcileController struct {
	targets    ports.ReconcileTargetLister
	store      ports.WorkloadInstanceStore
	reader     ports.WorkloadProviderStatusReader
	reconciler ports.WorkloadStatusReconciler
	config     ports.ReconcileControllerConfig
	now        func() time.Time
	mu         sync.Mutex
	backoff    map[string]time.Time
	metrics    ports.ReconcileControllerMetrics
}

type ReconcileControllerOption func(*LocalWorkloadReconcileController)

func WithReconcileControllerClock(now func() time.Time) ReconcileControllerOption {
	return func(controller *LocalWorkloadReconcileController) {
		if now != nil {
			controller.now = now
		}
	}
}

func NewLocalWorkloadReconcileController(
	targets ports.ReconcileTargetLister,
	store ports.WorkloadInstanceStore,
	reader ports.WorkloadProviderStatusReader,
	reconciler ports.WorkloadStatusReconciler,
	config ports.ReconcileControllerConfig,
	options ...ReconcileControllerOption,
) *LocalWorkloadReconcileController {
	controller := &LocalWorkloadReconcileController{
		targets:    targets,
		store:      store,
		reader:     reader,
		reconciler: reconciler,
		config:     defaultReconcileControllerConfig(config),
		now:        time.Now,
		backoff:    map[string]time.Time{},
	}
	for _, option := range options {
		option(controller)
	}
	return controller
}

func (c *LocalWorkloadReconcileController) Metrics() ports.ReconcileControllerMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.metrics
}

func (c *LocalWorkloadReconcileController) Start(ctx context.Context) error {
	if err := c.validate(); err != nil {
		return err
	}
	for {
		active, err := c.runOnce(ctx)
		if err != nil {
			return err
		}
		interval := time.Duration(c.config.NormalIntervalSeconds) * time.Second
		if active {
			interval = time.Duration(c.config.ActiveIntervalSeconds) * time.Second
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (c *LocalWorkloadReconcileController) ReconcileNow(ctx context.Context, target ports.ReconcileTarget) (ports.ReconcileResult, error) {
	if err := c.validate(); err != nil {
		return ports.ReconcileResult{}, err
	}
	if target.TenantID == "" || target.InstanceID == "" || target.Kind == "" {
		return ports.ReconcileResult{}, fmt.Errorf("%w: tenant_id/instance_id/kind required for reconcile target", ports.ErrInvalid)
	}
	tenantCtx, err := tenantScopedContext(ctx, target.TenantID)
	if err != nil {
		return ports.ReconcileResult{}, err
	}
	current, err := c.store.Get(tenantCtx, target.TenantID, target.InstanceID)
	if err != nil {
		return ports.ReconcileResult{}, err
	}
	apply := ports.WorkloadProviderApplyResult{
		Applied:      true,
		Provider:     firstNonEmpty(target.Provider, current.Provider),
		Operation:    reconcileOperationForState(current.Status.State),
		ResourceRefs: append([]string(nil), current.ResourceRefs...),
		AppliedAt:    c.now().UTC(),
	}
	previous := current.Status.State
	observation, err := c.reader.Observe(ctx, ports.WorkloadProviderStatusRequest{
		TenantID:    current.TenantID,
		InstanceID:  current.InstanceID,
		Kind:        current.Kind,
		ApplyResult: apply,
		RequestedAt: c.now().UTC(),
	})
	if err != nil {
		if errors.Is(err, ports.ErrNotFound) {
			return c.markProviderMissing(ctx, current)
		}
		return ports.ReconcileResult{}, err
	}
	reconciled, err := c.reconciler.Reconcile(ctx, ports.WorkloadReconcileRequest{
		AuditID:     current.AuditID,
		Current:     current.Status,
		ApplyResult: apply,
		Observation: observation,
	})
	if err != nil {
		return ports.ReconcileResult{}, err
	}
	if reconciled.Changed {
		current.Status = reconciled.Status
		current.UpdatedAt = firstNonZeroTime(reconciled.Status.UpdatedAt, reconciled.ReconciledAt, c.now().UTC())
		if err := c.store.UpsertStatus(tenantCtx, current); err != nil {
			return ports.ReconcileResult{}, err
		}
	}
	return ports.ReconcileResult{
		TenantID:      current.TenantID,
		InstanceID:    current.InstanceID,
		PreviousState: previous,
		CurrentState:  reconciled.Status.State,
		StateChanged:  reconciled.Changed,
		Reason:        reconciled.Reason,
		ReconciledAt:  reconciled.ReconciledAt,
	}, nil
}

func (c *LocalWorkloadReconcileController) runOnce(ctx context.Context) (bool, error) {
	c.recordTick()
	targets, err := c.targets.ListReconcileTargets(ctx, ports.ReconcileTargetListRequest{
		StaleBefore: c.now().UTC().Add(-time.Duration(c.config.StaleThresholdSeconds) * time.Second),
		Limit:       c.config.MaxConcurrentReconciles,
	})
	if err != nil {
		return false, err
	}
	active := false
	for _, target := range targets {
		if isTransientWorkloadState(target.State) {
			active = true
		}
		if c.isTargetBackedOff(target, c.now().UTC()) {
			c.recordBackoffSkip()
			continue
		}
		result, err := c.ReconcileNow(ctx, target)
		if err != nil {
			c.recordFailure(target, c.now().UTC())
			continue
		}
		c.recordSuccess(target)
		if isTransientWorkloadState(result.CurrentState) {
			active = true
		}
	}
	return active, nil
}

func (c *LocalWorkloadReconcileController) markProviderMissing(ctx context.Context, current ports.WorkloadInstanceRecord) (ports.ReconcileResult, error) {
	previous := current.Status.State
	now := c.now().UTC()
	current.Status.State = ports.WorkloadStateFailed
	current.Status.Reason = "ProviderResourceLost"
	current.Status.UpdatedAt = now
	current.UpdatedAt = now
	tenantCtx, err := tenantScopedContext(ctx, current.TenantID)
	if err != nil {
		return ports.ReconcileResult{}, err
	}
	if err := c.store.UpsertStatus(tenantCtx, current); err != nil {
		return ports.ReconcileResult{}, err
	}
	return ports.ReconcileResult{
		TenantID:        current.TenantID,
		InstanceID:      current.InstanceID,
		PreviousState:   previous,
		CurrentState:    ports.WorkloadStateFailed,
		StateChanged:    previous != ports.WorkloadStateFailed,
		ProviderMissing: true,
		Reason:          "ProviderResourceLost",
		ReconciledAt:    now,
	}, nil
}

func tenantScopedContext(ctx context.Context, tenantID string) (context.Context, error) {
	if _, ok := types.TryFromContext(ctx); ok {
		return ctx, nil
	}
	parsed, err := uuid.Parse(tenantID)
	if err != nil {
		// Local profile data may use non-UUID tenant ids; derive a stable UUID to satisfy RLS context contract.
		parsed = uuid.NewSHA1(uuid.NameSpaceOID, []byte(tenantID))
	}
	return types.WithTenant(ctx, &types.TenantContext{TenantID: parsed}), nil
}

func (c *LocalWorkloadReconcileController) validate() error {
	if c.targets == nil {
		return fmt.Errorf("%w: reconcile target lister is required", ports.ErrNotConfigured)
	}
	if c.store == nil {
		return fmt.Errorf("%w: workload instance store is required", ports.ErrNotConfigured)
	}
	if c.reader == nil {
		return fmt.Errorf("%w: workload provider status reader is required", ports.ErrNotConfigured)
	}
	if c.reconciler == nil {
		return fmt.Errorf("%w: workload status reconciler is required", ports.ErrNotConfigured)
	}
	return nil
}

func defaultReconcileControllerConfig(config ports.ReconcileControllerConfig) ports.ReconcileControllerConfig {
	if config.NormalIntervalSeconds <= 0 {
		config.NormalIntervalSeconds = 30
	}
	if config.ActiveIntervalSeconds <= 0 {
		config.ActiveIntervalSeconds = 5
	}
	if config.StaleThresholdSeconds <= 0 {
		config.StaleThresholdSeconds = 120
	}
	if config.MaxConcurrentReconciles <= 0 {
		config.MaxConcurrentReconciles = 10
	}
	if config.FailureBackoffSeconds <= 0 {
		config.FailureBackoffSeconds = 30
	}
	return config
}

func (c *LocalWorkloadReconcileController) isTargetBackedOff(target ports.ReconcileTarget, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.backoff[reconcileTargetKey(target)]
	return ok && now.Before(until)
}

func (c *LocalWorkloadReconcileController) recordTick() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics.Ticks++
}

func (c *LocalWorkloadReconcileController) recordSuccess(target ports.ReconcileTarget) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics.Successes++
	delete(c.backoff, reconcileTargetKey(target))
}

func (c *LocalWorkloadReconcileController) recordFailure(target ports.ReconcileTarget, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics.Failures++
	c.backoff[reconcileTargetKey(target)] = now.Add(time.Duration(c.config.FailureBackoffSeconds) * time.Second)
}

func (c *LocalWorkloadReconcileController) recordBackoffSkip() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics.SkippedBackoff++
}

func reconcileTargetKey(target ports.ReconcileTarget) string {
	return target.TenantID + "/" + string(target.Kind) + "/" + target.InstanceID
}

func reconcileOperationForState(state ports.WorkloadState) ports.WorkloadLifecycleAction {
	switch state {
	case ports.WorkloadStateStopped, ports.WorkloadStateStopping:
		return ports.WorkloadLifecycleStop
	case ports.WorkloadStateDeleting, ports.WorkloadStateDeleted:
		return ports.WorkloadLifecycleDelete
	case ports.WorkloadStateStarting, ports.WorkloadStateRunning:
		return ports.WorkloadLifecycleStart
	default:
		return ports.WorkloadLifecycleCreate
	}
}

func isTransientWorkloadState(state ports.WorkloadState) bool {
	switch state {
	case ports.WorkloadStatePending, ports.WorkloadStateProvisioning, ports.WorkloadStateStarting, ports.WorkloadStateStopping, ports.WorkloadStateDeleting:
		return true
	default:
		return false
	}
}

var _ ports.WorkloadReconcileController = (*LocalWorkloadReconcileController)(nil)
