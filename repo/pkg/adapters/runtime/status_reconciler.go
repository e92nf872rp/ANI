package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalStatusReconciler struct {
	now func() time.Time
}

type StatusReconcilerOption func(*LocalStatusReconciler)

func WithReconcileClock(now func() time.Time) StatusReconcilerOption {
	return func(reconciler *LocalStatusReconciler) {
		if now != nil {
			reconciler.now = now
		}
	}
}

func NewLocalStatusReconciler(options ...StatusReconcilerOption) *LocalStatusReconciler {
	reconciler := &LocalStatusReconciler{now: time.Now}
	for _, option := range options {
		option(reconciler)
	}
	return reconciler
}

func (r *LocalStatusReconciler) Reconcile(_ context.Context, request ports.WorkloadReconcileRequest) (ports.WorkloadReconcileResult, error) {
	if err := validateReconcileRequest(request); err != nil {
		return ports.WorkloadReconcileResult{}, err
	}

	nextState, err := mapProviderPhase(request.Observation.Phase)
	if err != nil {
		return ports.WorkloadReconcileResult{}, err
	}

	// Terminal-state guard: failed is terminal, but a pod that was in
	// CrashLoopBackOff (mapped to failed) may self-heal and report phase
	// "running". Allow failed→running so applyStateTransition can re-acquire
	// quota via a fresh TryManyTx (retryQuota). Block other failed→* transitions
	// (e.g. failed→provisioning) which have no meaningful TCC action.
	if request.Current.State == ports.WorkloadStateFailed && nextState != ports.WorkloadStateFailed && nextState != ports.WorkloadStateRunning {
		return ports.WorkloadReconcileResult{
			Status:       request.Current,
			Changed:      false,
			Reason:       "terminal state failed: ignoring provider phase " + request.Observation.Phase,
			ReconciledAt: r.now().UTC(),
		}, nil
	}

	status := request.Current
	status.State = nextState
	status.Endpoint = firstNonEmpty(request.Observation.Endpoint, status.Endpoint)
	status.NodeName = request.Observation.NodeName
	status.Reason = request.Observation.Reason
	if len(request.Observation.Networks) > 0 {
		status.Networks = request.Observation.Networks
	}
	if len(request.Observation.Storage) > 0 {
		status.Storage = request.Observation.Storage
	}
	status.UpdatedAt = firstNonZeroTime(request.Observation.ObservedAt, r.now().UTC())

	return ports.WorkloadReconcileResult{
		Status:       status,
		Changed:      statusChanged(request.Current, status),
		Reason:       "reconciled provider phase " + request.Observation.Phase,
		ReconciledAt: r.now().UTC(),
	}, nil
}

func validateReconcileRequest(request ports.WorkloadReconcileRequest) error {
	if strings.TrimSpace(request.AuditID) == "" {
		return fmt.Errorf("%w: audit id is required before status reconcile", ports.ErrInvalid)
	}
	if !request.ApplyResult.Applied {
		return fmt.Errorf("%w: provider apply must be applied before status reconcile", ports.ErrInvalid)
	}
	if request.Current.Ref.TenantID == "" {
		return fmt.Errorf("%w: current workload tenant id is required", ports.ErrInvalid)
	}
	if request.Current.Ref.InstanceID == "" {
		return fmt.Errorf("%w: current workload instance id is required", ports.ErrInvalid)
	}
	if request.Observation.TenantID != request.Current.Ref.TenantID {
		return fmt.Errorf("%w: observation tenant does not match workload", ports.ErrInvalid)
	}
	if request.Observation.InstanceID != request.Current.Ref.InstanceID {
		return fmt.Errorf("%w: observation instance does not match workload", ports.ErrInvalid)
	}
	if request.Observation.Kind != "" && request.Observation.Kind != request.Current.Ref.Kind {
		return fmt.Errorf("%w: observation kind does not match workload", ports.ErrInvalid)
	}
	if request.Observation.Provider == "" {
		return fmt.Errorf("%w: observation provider is required", ports.ErrInvalid)
	}
	if request.ApplyResult.Provider != "" && request.ApplyResult.Provider != request.Observation.Provider {
		return fmt.Errorf("%w: observation provider does not match apply result", ports.ErrInvalid)
	}
	if len(request.ApplyResult.ResourceRefs) == 0 {
		return fmt.Errorf("%w: apply resource refs are required before status reconcile", ports.ErrInvalid)
	}
	if len(request.Observation.ResourceRefs) == 0 {
		return fmt.Errorf("%w: observation resource refs are required", ports.ErrInvalid)
	}
	if !resourceRefsOverlap(request.ApplyResult.ResourceRefs, request.Observation.ResourceRefs) {
		return fmt.Errorf("%w: observation resource refs do not match apply result", ports.ErrInvalid)
	}
	return nil
}

func mapProviderPhase(phase string) (ports.WorkloadState, error) {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "pending", "creating", "provisioning", "scheduled", "starting", "waitingforvolumebinding":
		return ports.WorkloadStateProvisioning, nil
	case "ready", "running", "started":
		return ports.WorkloadStateRunning, nil
	case "stopping", "terminating":
		return ports.WorkloadStateStopping, nil
	case "stopped", "succeeded", "completed", "halted":
		return ports.WorkloadStateStopped, nil
	case "deleting":
		return ports.WorkloadStateDeleting, nil
	case "deleted":
		return ports.WorkloadStateDeleted, nil
	case "failed", "error", "crashloopbackoff", "errimagepull", "imagepullbackoff", "pvcnotfound", "datavolumeerror":
		return ports.WorkloadStateFailed, nil
	default:
		return "", fmt.Errorf("%w: unsupported provider phase %q", ports.ErrUnsupported, phase)
	}
}

func resourceRefsOverlap(left []string, right []string) bool {
	refs := make(map[string]struct{}, len(left))
	for _, ref := range left {
		refs[ref] = struct{}{}
	}
	for _, ref := range right {
		if _, ok := refs[ref]; ok {
			return true
		}
	}
	return false
}

func statusChanged(before ports.WorkloadStatus, after ports.WorkloadStatus) bool {
	return before.State != after.State ||
		before.Endpoint != after.Endpoint ||
		before.NodeName != after.NodeName ||
		before.Reason != after.Reason
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value.UTC()
		}
	}
	return time.Time{}
}

var _ ports.WorkloadStatusReconciler = (*LocalStatusReconciler)(nil)
