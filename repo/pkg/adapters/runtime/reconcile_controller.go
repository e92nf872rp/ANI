package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// defaultProvisioningTimeoutMin is the fallback provisioning timeout in
// minutes when PROVISIONING_TIMEOUT_MIN is unset or invalid.
const defaultProvisioningTimeoutMin = 10

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
	// quotaService performs TCC Confirm/Cancel/Release inside a tenant
	// transaction. nil means GPU quota is disabled and all quota calls are
	// no-ops (SPEC §5.1 GPU_QUOTA_ENABLED=false case).
	quotaService ports.QuotaService
	// metadataStore opens tenant-scoped transactions for the quota + status
	// atomic write. nil falls back to the non-transactional store.UpsertStatus.
	metadataStore ports.MetadataStore
	// storeTx writes instance status inside an externally-owned MetadataTx
	// (same transaction as Confirm/Cancel/Release). nil falls back to the
	// non-transactional store.UpsertStatus.
	storeTx ports.WorkloadInstanceStoreTx
	// provisioningTimeoutMin is the provisioning deadline. A non-terminal
	// instance older than this is marked failed and its reserved quota is
	// cancelled. Configured via PROVISIONING_TIMEOUT_MIN, default 10.
	provisioningTimeoutMin int
	// outboxWriter emits outbox events inside the same tenant transaction
	// as the quota + status write (plan.md §6.3.2). nil skips outbox events.
	outboxWriter OutboxWriter
}

type ReconcileControllerOption func(*LocalWorkloadReconcileController)

func WithReconcileControllerClock(now func() time.Time) ReconcileControllerOption {
	return func(controller *LocalWorkloadReconcileController) {
		if now != nil {
			controller.now = now
		}
	}
}

// WithQuotaService injects the TCC quota service used for Confirm/Cancel/
// Release on state transitions. When nil, quota calls are skipped and only
// status updates happen.
func WithQuotaService(service ports.QuotaService) ReconcileControllerOption {
	return func(controller *LocalWorkloadReconcileController) {
		controller.quotaService = service
	}
}

// WithMetadataStore injects the tenant transaction store used so Confirm/
// Cancel/Release and the status write commit atomically.
func WithMetadataStore(store ports.MetadataStore) ReconcileControllerOption {
	return func(controller *LocalWorkloadReconcileController) {
		controller.metadataStore = store
	}
}

// WithWorkloadInstanceStoreTx injects the transactional status writer used
// inside the same tenant transaction as Confirm/Cancel/Release.
func WithWorkloadInstanceStoreTx(storeTx ports.WorkloadInstanceStoreTx) ReconcileControllerOption {
	return func(controller *LocalWorkloadReconcileController) {
		controller.storeTx = storeTx
	}
}

// WithProvisioningTimeoutMin overrides the provisioning timeout (minutes).
func WithProvisioningTimeoutMin(minutes int) ReconcileControllerOption {
	return func(controller *LocalWorkloadReconcileController) {
		if minutes > 0 {
			controller.provisioningTimeoutMin = minutes
		}
	}
}

// WithOutboxWriter injects the outbox event writer used to emit lifecycle
// events (instance.confirmed, instance.cancelled, instance.released,
// instance.deleted, instance.create_failed) inside the same tenant
// transaction as the quota + status write (plan.md §6.3.2). When nil,
// outbox events are skipped.
func WithOutboxWriter(w OutboxWriter) ReconcileControllerOption {
	return func(controller *LocalWorkloadReconcileController) {
		controller.outboxWriter = w
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
		targets:                targets,
		store:                  store,
		reader:                 reader,
		reconciler:             reconciler,
		config:                 defaultReconcileControllerConfig(config),
		now:                    time.Now,
		backoff:                map[string]time.Time{},
		provisioningTimeoutMin: provisioningTimeoutFromEnv(),
	}
	for _, option := range options {
		option(controller)
	}
	return controller
}

// provisioningTimeoutFromEnv reads PROVISIONING_TIMEOUT_MIN. Falls back to
// defaultProvisioningTimeoutMin when unset or invalid.
func provisioningTimeoutFromEnv() int {
	raw := os.Getenv("PROVISIONING_TIMEOUT_MIN")
	if raw == "" {
		return defaultProvisioningTimeoutMin
	}
	minutes, err := strconv.Atoi(raw)
	if err != nil || minutes <= 0 {
		return defaultProvisioningTimeoutMin
	}
	return minutes
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
	ctx = reconcileTargetTenantContext(ctx, target.TenantID)
	current, err := c.store.Get(ctx, target.TenantID, target.InstanceID)
	if err != nil {
		return ports.ReconcileResult{}, err
	}
	slog.Info("reconcile target loaded",
		"instance_id", current.InstanceID,
		"state", current.Status.State,
		"quota_tx_ids", current.QuotaTxIDs,
		"resource_refs", current.ResourceRefs,
	)
	if current.Status.State == ports.WorkloadStateDeleting || current.Status.State == ports.WorkloadStateDeleted {
		return ports.ReconcileResult{
			TenantID:      current.TenantID,
			InstanceID:    current.InstanceID,
			PreviousState: current.Status.State,
			CurrentState:  current.Status.State,
			Reason:        current.Status.Reason,
			ReconciledAt:  c.now().UTC(),
		}, nil
	}
	// Provisioning timeout (SPEC §5.1): a non-terminal instance whose
	// CreatedAt is older than the configured deadline is marked failed and
	// its reserved quota is cancelled within a tenant transaction.
	if isTransientWorkloadState(current.Status.State) {
		if result, timedOut, err := c.checkProvisioningTimeout(ctx, current); err != nil {
			return ports.ReconcileResult{}, err
		} else if timedOut {
			return result, nil
		}
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
	// Enrich the observation for non-terminal GPU workloads: distinguish a
	// pod queued in the Volcano scheduler (Pending + scheduled=false) from a
	// real scheduling failure (Failed scheduling). The reconciler maps the
	// resulting Reason onto the workload status so the API can surface it.
	c.enrichProvisioningObservation(current, &observation)
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
		slog.Info("reconcile state transition",
			"instance_id", current.InstanceID,
			"previous", previous,
			"next", reconciled.Status.State,
			"quota_tx_ids", current.QuotaTxIDs,
			"has_tx_quota", c.hasTransactionalQuotaSupport(),
		)
		if err := c.applyStateTransition(ctx, current, previous, reconciled.Status.State); err != nil {
			slog.Error("reconcile state transition failed",
				"instance_id", current.InstanceID,
				"previous", previous,
				"next", reconciled.Status.State,
				"err", err,
			)
			return ports.ReconcileResult{}, err
		}
		slog.Info("reconcile state transition done",
			"instance_id", current.InstanceID,
			"previous", previous,
			"next", reconciled.Status.State,
		)
	} else if reconciled.Status.State == ports.WorkloadStateRunning &&
		len(current.QuotaTxIDs) > 0 && c.hasTransactionalQuotaSupport() {
		// Self-heal: when the instance is already running but the reconciler
		// didn't detect a state change (Changed=false), the quota may still
		// be stuck in "reserved" if the original Confirm was rolled back
		// (e.g. by an outbox write failure). Attempt an idempotent Confirm
		// (no status write) to fix the stuck reserved→used transition.
		// Confirm is a no-op when the reservation is already confirmed.
		if err := c.selfHealConfirm(ctx, current); err != nil {
			slog.Error("reconcile self-heal Confirm failed",
				"instance_id", current.InstanceID,
				"err", err,
			)
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

// applyStateTransition writes the new status and, when quota is enabled,
// performs the TCC action matching the state transition inside the same
// tenant transaction (SPEC §5.1):
//   - pending/provisioning -> running: Confirm (reserved -> used)
//   - pending/provisioning -> failed:  Cancel  (release reserved)
//   - running -> failed:               Release (release used)
//   - deleting -> deleted:             Cancel + Release (double-call, state-independent)
//
// When metadataStore/storeTx/quotaService are not configured the method falls
// back to the non-transactional store.UpsertStatus path for backward
// compatibility. Empty QuotaTxIDs skip the quota call (GPU_QUOTA_ENABLED=false).
func (c *LocalWorkloadReconcileController) applyStateTransition(ctx context.Context, record ports.WorkloadInstanceRecord, previous, next ports.WorkloadState) error {
	if !c.hasTransactionalQuotaSupport() {
		return c.store.UpsertStatus(ctx, record)
	}
	switch {
	case (previous == ports.WorkloadStatePending || previous == ports.WorkloadStateProvisioning) && next == ports.WorkloadStateRunning:
		return c.confirmQuota(ctx, record)
	case (previous == ports.WorkloadStatePending || previous == ports.WorkloadStateProvisioning) && next == ports.WorkloadStateFailed:
		return c.cancelQuota(ctx, record)
	case previous == ports.WorkloadStateRunning && next == ports.WorkloadStateFailed:
		return c.releaseQuota(ctx, record)
	case previous == ports.WorkloadStateFailed && next == ports.WorkloadStateRunning:
		return c.retryQuota(ctx, record)
	case previous == ports.WorkloadStateDeleting && next == ports.WorkloadStateDeleted:
		return c.CancelQuotaAndFinalize(ctx, record)
	default:
		// No TCC action for this transition; still persist status within a
		// tenant transaction so the write is consistent with any concurrent
		// quota write.
		slog.Info("applyStateTransition: no TCC action (default branch)",
			"instance_id", record.InstanceID,
			"previous", previous,
			"next", next,
			"quota_tx_ids", record.QuotaTxIDs,
		)
		return c.upsertStatusInTx(ctx, record)
	}
}

// hasTransactionalQuotaSupport reports whether the controller can perform
// transactional quota + status writes.
func (c *LocalWorkloadReconcileController) hasTransactionalQuotaSupport() bool {
	return c.metadataStore != nil && c.storeTx != nil
}

func reconcileTargetTenantContext(ctx context.Context, tenantID string) context.Context {
	if _, ok := types.TryFromContext(ctx); ok {
		return ctx
	}
	parsed, err := uuid.Parse(tenantID)
	if err != nil {
		return ctx
	}
	return types.WithTenant(ctx, &types.TenantContext{TenantID: parsed})
}

func (c *LocalWorkloadReconcileController) runOnce(ctx context.Context) (bool, error) {
	c.recordTick()
	targets, err := c.targets.ListReconcileTargets(ctx, ports.ReconcileTargetListRequest{
		StaleBefore: c.now().UTC().Add(-time.Duration(c.config.StaleThresholdSeconds) * time.Second),
		Limit:       c.config.MaxConcurrentReconciles,
	})
	if err != nil {
		slog.Error("reconcile list targets failed", "err", err)
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
			slog.Error("reconcile target failed", "instance_id", target.InstanceID, "err", err)
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
	if err := c.applyStateTransition(ctx, current, previous, ports.WorkloadStateFailed); err != nil {
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

// isGPUWorkloadKind reports whether the kind is backed by GPU scheduling and
// therefore subject to the queued/scheduling-failed distinction.
func isGPUWorkloadKind(kind ports.WorkloadKind) bool {
	switch kind {
	case ports.WorkloadKindGPUContainer, ports.WorkloadKindInference, ports.WorkloadKindNotebook, ports.WorkloadKindBatchJob:
		return true
	default:
		return false
	}
}

// enrichProvisioningObservation reads pod scheduling signals from the
// observation and sets a human-readable Reason that distinguishes a workload
// queued in the Volcano scheduler from one that failed scheduling. This phase
// only enriches the reason string; the reconciler still owns state mapping.
//
// Heuristics (SPEC §5.1 Pod Events):
//   - Phase "Pending" + empty NodeName + no failure keyword in Reason  -> queued
//   - Phase "Failed" / "CrashLoopBackOff" / "ImagePullBackOff" / "FailedScheduling" -> scheduling failed
func (c *LocalWorkloadReconcileController) enrichProvisioningObservation(record ports.WorkloadInstanceRecord, observation *ports.WorkloadProviderObservation) {
	if !isGPUWorkloadKind(record.Kind) {
		return
	}
	if !isTransientWorkloadState(record.Status.State) && record.Status.State != ports.WorkloadStateFailed {
		return
	}
	phase := observation.Phase
	reason := observation.Reason
	if isSchedulingFailure(phase, reason) {
		if observation.Reason == "" || observation.Reason == "observed by local provider status reader" {
			observation.Reason = "SchedulingFailed"
		}
		return
	}
	if isQueued(phase, reason, observation.NodeName) {
		if observation.Reason == "" || observation.Reason == "observed by local provider status reader" {
			observation.Reason = "QueuedInScheduler"
		}
	}
}

func isSchedulingFailure(phase, reason string) bool {
	switch phase {
	case "Failed", "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull":
		return true
	}
	return containsAny(reason, "FailedScheduling", "Insufficient", "node(s) had taints", "pod didn't trigger scale-up")
}

func isQueued(phase, reason, nodeName string) bool {
	if phase != "Pending" && phase != "Provisioning" && phase != "Creating" {
		return false
	}
	if nodeName != "" {
		return false
	}
	if isSchedulingFailure(phase, reason) {
		return false
	}
	return true
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if needle != "" && strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

// checkProvisioningTimeout marks a non-terminal instance failed when its
// CreatedAt exceeds the provisioning deadline. The reserved quota is cancelled
// inside the same tenant transaction as the status write. Returns timedOut=
// true when the timeout fired (and the result to return), false to continue
// normal reconciliation.
func (c *LocalWorkloadReconcileController) checkProvisioningTimeout(ctx context.Context, current ports.WorkloadInstanceRecord) (ports.ReconcileResult, bool, error) {
	deadline := current.CreatedAt.Add(time.Duration(c.provisioningTimeoutMin) * time.Minute)
	if !c.now().UTC().After(deadline) {
		return ports.ReconcileResult{}, false, nil
	}
	previous := current.Status.State
	now := c.now().UTC()
	current.Status.State = ports.WorkloadStateFailed
	current.Status.Reason = "ProvisioningTimeout"
	current.Status.UpdatedAt = now
	current.UpdatedAt = now
	if err := c.markProvisioningFailed(ctx, current); err != nil {
		return ports.ReconcileResult{}, false, err
	}
	return ports.ReconcileResult{
		TenantID:      current.TenantID,
		InstanceID:    current.InstanceID,
		PreviousState: previous,
		CurrentState:  ports.WorkloadStateFailed,
		StateChanged:  previous != ports.WorkloadStateFailed,
		Reason:        "ProvisioningTimeout",
		ReconciledAt:  now,
	}, true, nil
}

// markProvisioningFailed writes the failed status and cancels any reserved
// quota within a single tenant transaction. When transactional support is not
// configured it falls back to the non-transactional store. The Cancel call is
// skipped when QuotaTxIDs is empty (GPU_QUOTA_ENABLED=false).
func (c *LocalWorkloadReconcileController) markProvisioningFailed(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	if !c.hasTransactionalQuotaSupport() {
		return c.store.UpsertStatus(ctx, record)
	}
	return c.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
		if c.quotaService != nil && len(record.QuotaTxIDs) > 0 {
			if err := c.quotaService.Cancel(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
		}
		if err := c.writeOutbox(txCtx, tx, "instance.create_failed", record); err != nil {
			return err
		}
		return c.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// confirmQuota confirms the TCC reservation and writes the running status in
// one tenant transaction. Used for pending -> running.
func (c *LocalWorkloadReconcileController) confirmQuota(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	slog.Info("confirmQuota called",
		"instance_id", record.InstanceID,
		"quota_tx_ids", record.QuotaTxIDs,
		"quota_service_nil", c.quotaService == nil,
	)
	return c.runQuotaTransition(ctx, record, func(txCtx context.Context, tx ports.MetadataTx) error {
		if c.quotaService != nil && len(record.QuotaTxIDs) > 0 {
			if err := c.quotaService.Confirm(txCtx, tx, record.QuotaTxIDs, record.InstanceID); err != nil {
				slog.Error("confirmQuota Confirm failed",
					"instance_id", record.InstanceID,
					"quota_tx_ids", record.QuotaTxIDs,
					"err", err,
				)
				return err
			}
			slog.Info("confirmQuota Confirm succeeded",
				"instance_id", record.InstanceID,
				"quota_tx_ids", record.QuotaTxIDs,
			)
		} else {
			slog.Warn("confirmQuota skipped (quotaService nil or no QuotaTxIDs)",
				"instance_id", record.InstanceID,
				"quota_service_nil", c.quotaService == nil,
				"quota_tx_ids_len", len(record.QuotaTxIDs),
			)
		}
		if err := c.writeOutbox(txCtx, tx, "instance.confirmed", record); err != nil {
			return err
		}
		return c.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// selfHealConfirm attempts an idempotent TCC Confirm for a running instance
// whose quota may be stuck in "reserved" (e.g. the original Confirm was
// rolled back by an outbox write failure). Unlike confirmQuota it does NOT
// write the instance status — it only executes the Confirm SQL so the
// reserved→used transition is fixed without touching updated_at.
// To avoid unnecessary DB load on every reconcile tick, it first checks
// whether any txID is still in "reserved" state; if all are already
// confirmed/cancelled, it skips the Confirm call entirely.
func (c *LocalWorkloadReconcileController) selfHealConfirm(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	if c.quotaService == nil || len(record.QuotaTxIDs) == 0 {
		return nil
	}
	return c.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
		// Fast path: check if any txID is still reserved before calling Confirm.
		hasReserved, err := c.anyReservationReserved(txCtx, tx, record.QuotaTxIDs)
		if err != nil {
			slog.Warn("selfHealConfirm: failed to check reservation state, falling back to Confirm",
				"instance_id", record.InstanceID,
				"err", err,
			)
		} else if !hasReserved {
			return nil
		}
		if err := c.quotaService.Confirm(txCtx, tx, record.QuotaTxIDs, record.InstanceID); err != nil {
			return err
		}
		return nil
	})
}

// anyReservationReserved checks whether any of the given txIDs still has
// a reservation row in "reserved" state. Returns true if at least one is
// reserved, false if all are confirmed/cancelled or no rows exist.
func (c *LocalWorkloadReconcileController) anyReservationReserved(ctx context.Context, tx ports.MetadataTx, txIDs []string) (bool, error) {
	if len(txIDs) == 0 {
		return false, nil
	}
	var count int
	err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM resource_reservations
		WHERE tx_id = ANY($1) AND state = 'reserved'
	`, txIDs).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// cancelQuota cancels the TCC reservation and writes the failed status in one
// tenant transaction. Used for pending/provisioning -> failed.
//
// When the reservation is already confirmed (e.g. self-heal Confirm ran
// before the failure was detected), a plain Cancel would skip it (state !=
// 'reserved') and leak used quota. To prevent this, cancelQuota falls back to
// Release when the reservation is in 'confirmed' state, ensuring used is
// decremented correctly.
func (c *LocalWorkloadReconcileController) cancelQuota(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	return c.runQuotaTransition(ctx, record, func(txCtx context.Context, tx ports.MetadataTx) error {
		if c.quotaService != nil && len(record.QuotaTxIDs) > 0 {
			// Try Cancel first (handles reserved → cancelled).
			if err := c.quotaService.Cancel(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
			// If any reservation was already confirmed (not reserved), Cancel
			// skipped it. Fall back to Release to decrement used.
			if err := c.quotaService.Release(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
		}
		if err := c.writeOutbox(txCtx, tx, "instance.cancelled", record); err != nil {
			return err
		}
		return c.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// releaseQuota releases the TCC reservation and writes the failed status in
// one tenant transaction. Used for running -> failed.
//
// When the reservation is still in 'reserved' state (e.g. the pod crashed
// before selfHealConfirm ran), a plain Release would skip it (WHERE
// state='confirmed') and leak reserved quota. To prevent this, releaseQuota
// calls Cancel first (releases reserved) then Release (releases used),
// matching cancelQuota's double-call approach. Both are idempotent.
func (c *LocalWorkloadReconcileController) releaseQuota(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	return c.runQuotaTransition(ctx, record, func(txCtx context.Context, tx ports.MetadataTx) error {
		if c.quotaService != nil && len(record.QuotaTxIDs) > 0 {
			if err := c.quotaService.Cancel(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
			if err := c.quotaService.Release(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
		}
		if err := c.writeOutbox(txCtx, tx, "instance.released", record); err != nil {
			return err
		}
		return c.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// retryQuota re-acquires quota for a failed→running transition. When the
// instance went running→failed the TCC reservation was released; if the pod
// self-heals (CrashLoopBackOff→Running) the reconciler observes failed→running.
// A fresh TryManyTx is needed so the reconciler can later Confirm the new
// reservation (reserved→used). The new QuotaTxIDs are written to the instance
// record in the same transaction. TryManyTx's SQL guard prevents oversell.
func (c *LocalWorkloadReconcileController) retryQuota(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	slog.Info("retryQuota called",
		"instance_id", record.InstanceID,
		"quota_tx_ids", record.QuotaTxIDs,
		"quota_service_nil", c.quotaService == nil,
	)
	return c.runQuotaTransition(ctx, record, func(txCtx context.Context, tx ports.MetadataTx) error {
		if c.quotaService != nil {
			gpuCount := 1
			if record.GPU != nil && record.GPU.Count > 0 {
				gpuCount = record.GPU.Count
			}
			reservations, err := c.quotaService.TryManyTx(txCtx, tx, []ports.QuotaTryRequest{{
				TenantID:     record.TenantID,
				ResourceType: ports.QuotaGPUCount,
				Amount:       int64(gpuCount),
			}})
			if err != nil {
				slog.Error("retryQuota TryManyTx failed",
					"instance_id", record.InstanceID,
					"err", err,
				)
				return err
			}
			record.QuotaTxIDs = reservationTxIDs(reservations)
			slog.Info("retryQuota TryManyTx succeeded",
				"instance_id", record.InstanceID,
				"new_quota_tx_ids", record.QuotaTxIDs,
			)
		}
		if err := c.writeOutbox(txCtx, tx, "instance.retried", record); err != nil {
			return err
		}
		return c.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// upsertStatusInTx writes status within a tenant transaction without any
// quota action. Used for transitions that have no TCC mapping.
func (c *LocalWorkloadReconcileController) upsertStatusInTx(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	return c.runQuotaTransition(ctx, record, func(txCtx context.Context, tx ports.MetadataTx) error {
		return c.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// runQuotaTransition opens a tenant transaction and runs the provided action.
// It is the shared boundary for all same-transaction quota + status writes.
func (c *LocalWorkloadReconcileController) runQuotaTransition(ctx context.Context, record ports.WorkloadInstanceRecord, action func(context.Context, ports.MetadataTx) error) error {
	if !c.hasTransactionalQuotaSupport() {
		return c.store.UpsertStatus(ctx, record)
	}
	return c.metadataStore.WithTenantTx(ctx, action)
}

// writeOutbox emits an outbox event inside the given MetadataTx. It is a
// no-op when outboxWriter is nil (outbox disabled). The event_type follows
// plan.md §6.3.2 conventions: instance.confirmed, instance.cancelled,
// instance.released, instance.deleted, instance.create_failed.
//
// writeOutbox is best-effort: when the outbox write fails (e.g. aggregate_id
// is not a valid UUID) it logs a warning but does NOT return an error. This
// prevents outbox failures from rolling back the quota Confirm/Cancel/Release
// and status write, which are the critical operations in the transaction.
func (c *LocalWorkloadReconcileController) writeOutbox(ctx context.Context, tx ports.MetadataTx, eventType string, record ports.WorkloadInstanceRecord) error {
	if c.outboxWriter == nil {
		return nil
	}
	// The outbox_events table casts aggregate_id and tenant_id to UUID.
	// instance_id is "inst_<uuid>" which is NOT a valid UUID, so the INSERT
	// would fail with SQLSTATE 22P02 and abort the entire PostgreSQL
	// transaction — rolling back the quota Confirm/Cancel/Release and status
	// write that already executed in the same tx. Extract the UUID part
	// from "inst_<uuid>" before writing; skip the outbox write entirely
	// when no valid UUID can be extracted.
	aggregateID := extractUUIDFromInstanceID(record.InstanceID)
	if aggregateID == "" {
		slog.Warn("writeOutbox: instance_id has no valid UUID, skipping outbox",
			"instance_id", record.InstanceID,
			"event_type", eventType,
		)
		return nil
	}
	if _, err := uuid.Parse(record.TenantID); err != nil {
		slog.Warn("writeOutbox: tenant_id is not a valid UUID, skipping outbox",
			"tenant_id", record.TenantID,
			"event_type", eventType,
		)
		return nil
	}
	payload, err := encodeOutboxPayload(map[string]any{
		"instance_id": record.InstanceID,
		"kind":        string(record.Kind),
		"state":       string(record.Status.State),
		"reason":      record.Status.Reason,
	})
	if err != nil {
		slog.Warn("writeOutbox: encode payload failed, skipping outbox",
			"instance_id", record.InstanceID,
			"event_type", eventType,
			"err", err,
		)
		return nil
	}
	if err := c.outboxWriter.WriteTx(ctx, tx, OutboxEvent{
		AggregateType: "workload_instance",
		AggregateID:   aggregateID,
		EventType:     eventType,
		TenantID:      record.TenantID,
		Payload:       payload,
	}); err != nil {
		slog.Warn("writeOutbox: outbox write failed, skipping (quota+status still commit)",
			"instance_id", record.InstanceID,
			"event_type", eventType,
			"err", err,
		)
		return nil
	}
	return nil
}

// extractUUIDFromInstanceID extracts the UUID part from an instance_id
// formatted as "inst_<uuid>". Returns empty string when no valid UUID is found.
func extractUUIDFromInstanceID(instanceID string) string {
	raw := strings.TrimPrefix(instanceID, "inst_")
	if _, err := uuid.Parse(raw); err != nil {
		return ""
	}
	return raw
}

// CancelQuotaAndFinalize releases both reserved and used quota and writes the
// final (deleted) status in one tenant transaction. It is independent of the
// original instance state so a single call covers pending/running/failed
// (SPEC §5.1 delete flow). When quota is disabled it only writes the status.
func (c *LocalWorkloadReconcileController) CancelQuotaAndFinalize(ctx context.Context, record ports.WorkloadInstanceRecord) error {
	ctx = reconcileTargetTenantContext(ctx, record.TenantID)
	now := c.now().UTC()
	record.Status.State = ports.WorkloadStateDeleted
	record.Status.UpdatedAt = now
	record.UpdatedAt = now
	if !c.hasTransactionalQuotaSupport() {
		return c.store.UpsertStatus(ctx, record)
	}
	return c.metadataStore.WithTenantTx(ctx, func(txCtx context.Context, tx ports.MetadataTx) error {
		if c.quotaService != nil && len(record.QuotaTxIDs) > 0 {
			// Cancel releases reserved (if any, idempotent); Release releases
			// used (if any, idempotent). Both are safe to call regardless of
			// the original reservation state.
			if err := c.quotaService.Cancel(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
			if err := c.quotaService.Release(txCtx, tx, record.QuotaTxIDs); err != nil {
				return err
			}
		}
		if err := c.writeOutbox(txCtx, tx, "instance.deleted", record); err != nil {
			return err
		}
		return c.storeTx.UpsertStatusTx(txCtx, tx, record)
	})
}

// ReconcileQuota is the quota reconciliation loop (SPEC §5.1 AC #6). For each
// tenant target it computes the actual GPU usage from non-terminal provider
// pods and compares it against the quota store (PG) view. This phase only
// logs warnings — it does NOT correct the quota ledger. Correction is a
// later, gated capability.
func (c *LocalWorkloadReconcileController) ReconcileQuota(ctx context.Context, targets []ports.ReconcileTarget) {
	for _, target := range targets {
		c.reconcileQuotaForTarget(ctx, target)
	}
}

func (c *LocalWorkloadReconcileController) reconcileQuotaForTarget(ctx context.Context, target ports.ReconcileTarget) {
	if c.quotaService == nil {
		return
	}
	record, err := c.store.Get(ctx, target.TenantID, target.InstanceID)
	if err != nil {
		slog.Warn("quota reconcile: instance not found",
			"tenant_id", target.TenantID, "instance_id", target.InstanceID, "err", err)
		return
	}
	if isTerminalWorkloadState(record.Status.State) {
		return
	}
	if !isGPUWorkloadKind(record.Kind) {
		return
	}
	// AC #6: compute actual used from non-terminal GPU pods and compare with
	// the quota ledger. Only log warnings this phase (no correction).
	observation, err := c.reader.Observe(ctx, ports.WorkloadProviderStatusRequest{
		TenantID:   record.TenantID,
		InstanceID: record.InstanceID,
		Kind:       record.Kind,
		ApplyResult: ports.WorkloadProviderApplyResult{
			Applied:      true,
			Provider:     firstNonEmpty(target.Provider, record.Provider),
			Operation:    reconcileOperationForState(record.Status.State),
			ResourceRefs: append([]string(nil), record.ResourceRefs...),
			AppliedAt:    c.now().UTC(),
		},
		RequestedAt: c.now().UTC(),
	})
	if err != nil {
		slog.Warn("quota reconcile: observe failed",
			"tenant_id", record.TenantID, "instance_id", record.InstanceID, "err", err)
		return
	}
	actualUsed := observation.GPUCount
	// The quota ledger stores the requested (reserved) GPU count on the
	// instance; a non-terminal pod that is actually running should match it.
	// Only warn when there is a real divergence — this phase does not correct.
	expectedReserved := 0
	if record.GPU != nil {
		expectedReserved = record.GPU.Count
	}
	if actualUsed > 0 && actualUsed != expectedReserved {
		slog.Warn("quota reconcile: actual GPU usage diverges from ledger",
			"tenant_id", record.TenantID,
			"instance_id", record.InstanceID,
			"kind", record.Kind,
			"actual_used", actualUsed,
			"ledger_reserved", expectedReserved,
			"phase", "warning-only",
		)
	}
}

func isTerminalWorkloadState(state ports.WorkloadState) bool {
	switch state {
	case ports.WorkloadStateFailed, ports.WorkloadStateDeleted, ports.WorkloadStateStopped:
		return true
	default:
		return false
	}
}

var _ ports.WorkloadReconcileController = (*LocalWorkloadReconcileController)(nil)
