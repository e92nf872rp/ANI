package ports

import (
	"context"
	"time"
)

// ReconcileTarget identifies an instance that needs reconciliation.
type ReconcileTarget struct {
	TenantID       string
	InstanceID     string
	Kind           WorkloadKind
	State          WorkloadState
	Provider       string
	LastObservedAt time.Time
}

type ReconcileTargetListRequest struct {
	StaleBefore time.Time
	Limit       int
}

type ReconcileTargetLister interface {
	ListReconcileTargets(ctx context.Context, request ReconcileTargetListRequest) ([]ReconcileTarget, error)
}

// ReconcileResult holds the outcome of a single reconcile attempt.
type ReconcileResult struct {
	TenantID      string
	InstanceID    string
	PreviousState WorkloadState
	CurrentState  WorkloadState
	StateChanged  bool
	// ProviderMissing is true when the provider resource no longer exists
	// (e.g. K8s Deployment deleted out-of-band). The controller marks the
	// instance failed with reason ProviderResourceLost.
	ProviderMissing bool
	Reason          string
	ReconciledAt    time.Time
}

// ReconcileControllerConfig configures the background reconcile loop.
type ReconcileControllerConfig struct {
	// NormalIntervalSeconds is the polling interval when all instances are
	// in a stable terminal state. Default 30.
	NormalIntervalSeconds int
	// ActiveIntervalSeconds is the polling interval when at least one instance
	// is in a transient state (provisioning/starting/stopping/deleting).
	// Default 5.
	ActiveIntervalSeconds int
	// StaleThresholdSeconds marks an instance as priority-reconcile candidate
	// if its last observation is older than this value. Default 120.
	StaleThresholdSeconds int
	// MaxConcurrentReconciles limits the number of targets processed per tick.
	// Implementations may use it as a provider-call concurrency cap. Default 10.
	MaxConcurrentReconciles int
	// FailureBackoffSeconds is the cooldown before retrying a target after a
	// transient reconcile error. Default 30.
	FailureBackoffSeconds int
}

type ReconcileControllerMetrics struct {
	Ticks          int64
	Successes      int64
	Failures       int64
	SkippedBackoff int64
}

type ReconcileControllerMetricsReader interface {
	Metrics() ReconcileControllerMetrics
}

// ReconcileLeaderElector gates a reconcile controller so only the elected
// process runs the background loop in an HA deployment.
type ReconcileLeaderElector interface {
	Run(ctx context.Context, run func(context.Context) error) error
}

// WorkloadReconcileController is the background control-plane component that
// continuously aligns actual provider state (Kubernetes/KubeVirt/BM) with
// the desired state stored in workload_instances.
//
// Design contract (control plane / data plane separation):
//   - Runs independently of the HTTP request path. A gateway restart does not
//     interrupt reconciliation for running instances.
//   - On each tick: scan workload_instances for non-terminal or stale entries,
//     call WorkloadProviderStatusReader.Observe per instance, then call
//     WorkloadStatusReconciler.Reconcile to write the delta.
//   - MUST NOT create or delete provider resources — observe-and-align only.
//   - A provider observation that finds no matching resource transitions the
//     instance to failed/ProviderResourceLost, never silently ignores the gap.
//
// Reconcile loop sequence:
//
//	WorkloadInstanceStore.List(non-terminal or stale)
//	  ↓
//	WorkloadProviderStatusReader.Observe (per instance, up to MaxConcurrentReconciles per tick)
//	  ↓
//	WorkloadStatusReconciler.Reconcile
//	  ↓
//	WorkloadInstanceStore.UpsertStatus  (only when StateChanged)
//	  ↓
//	Sleep(ActiveInterval when transient instances exist, else NormalInterval)
type WorkloadReconcileController interface {
	// Start begins the reconcile loop. Blocks until ctx is cancelled.
	// Returns a non-nil error only on fatal initialisation failure.
	Start(ctx context.Context) error

	// ReconcileNow triggers an immediate out-of-band reconcile for a single
	// instance. Called after a lifecycle action is accepted to reduce the lag
	// before the new state is visible in the API.
	ReconcileNow(ctx context.Context, target ReconcileTarget) (ReconcileResult, error)
}
