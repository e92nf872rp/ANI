package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	runtimeport "github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

const (
	defaultLeaseDuration = 30 * time.Second
	defaultRetryDelay    = 5 * time.Second
)

type Worker struct {
	store         repository.Store
	runtime       runtimeport.InferenceRuntime
	owner         string
	now           func() time.Time
	leaseDuration time.Duration
	retryDelay    time.Duration
}

func NewWorker(store repository.Store, runtime runtimeport.InferenceRuntime, owner string, now func() time.Time) *Worker {
	if now == nil {
		now = time.Now
	}
	return &Worker{
		store: store, runtime: runtime, owner: owner, now: now,
		leaseDuration: defaultLeaseDuration, retryDelay: defaultRetryDelay,
	}
}

func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := w.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("inference reconciler tick failed", "err", err)
			}
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	now := w.now().UTC()
	operation, claimed, err := w.store.ClaimOperation(ctx, w.owner, now, w.leaseDuration)
	if err != nil || !claimed {
		return false, err
	}
	service, err := w.store.GetService(ctx, operation.TenantID, operation.ServiceID)
	if err != nil {
		return true, w.retry(ctx, operation, "SERVICE_LOOKUP_FAILED", err)
	}
	if service.Generation != operation.TargetGeneration || service.ActiveOperationID != operation.ID {
		return true, w.terminal(ctx, operation, "STALE_GENERATION", repository.ErrStaleGeneration)
	}
	runtimeKey := runtimeIdempotencyKey(operation.ServiceID, operation.TargetGeneration)
	if operation.Type == domain.ActionDelete {
		err := w.runtime.Delete(ctx, runtimeport.DeleteRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: runtimeKey,
		})
		if err != nil {
			return true, w.retry(ctx, operation, "RUNTIME_DELETE_FAILED", err)
		}
		_, observeErr := w.runtime.Observe(ctx, runtimeport.RuntimeIdentity{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
		})
		if observeErr == nil {
			return true, w.retry(ctx, operation, "RUNTIME_DELETE_PENDING", errors.New("runtime is still observable after delete"))
		}
		if !errors.Is(observeErr, runtimeport.ErrRuntimeNotFound) {
			return true, w.retry(ctx, operation, "RUNTIME_OBSERVE_FAILED", observeErr)
		}
		_, applyErr := w.apply(ctx, repository.Observation{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, OperationID: operation.ID,
			TargetGeneration: operation.TargetGeneration, Status: domain.StatusStopped,
			AppliedSpec: operation.TargetSpec, ReadyReplicas: 0, Complete: true, Deleted: true,
			LeaseToken: operation.LeaseToken,
		})
		return true, applyErr
	}

	accepted, err := w.applyRuntimeIntent(ctx, service, operation, runtimeKey)
	if err != nil && errors.Is(err, runtimeport.ErrRuntimeUnsupported) {
		return true, w.terminal(ctx, operation, "ACCELERATOR_SPEC_UNAVAILABLE", err)
	}
	runtimeRef := accepted.RuntimeRef
	if runtimeRef == uuid.Nil {
		runtimeRef = service.RuntimeRef
	}
	if service.RuntimeRef == uuid.Nil && runtimeRef != uuid.Nil {
		if stale, persistErr := w.apply(ctx, repository.Observation{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, OperationID: operation.ID,
			TargetGeneration: operation.TargetGeneration, Status: domain.StatusDeploying,
			AppliedSpec: operation.TargetSpec, RuntimeRef: runtimeRef, LeaseToken: operation.LeaseToken,
		}); persistErr != nil {
			return true, persistErr
		} else if stale {
			return true, nil
		}
	}
	if err != nil {
		return true, w.retry(ctx, operation, "RUNTIME_MUTATION_FAILED", err)
	}
	observed, observeErr := w.runtime.Observe(ctx, runtimeport.RuntimeIdentity{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: runtimeRef,
	})
	if operation.Type == domain.ActionStop {
		if observeErr != nil && !errors.Is(observeErr, runtimeport.ErrRuntimeNotFound) {
			return true, w.retry(ctx, operation, "RUNTIME_OBSERVE_FAILED", observeErr)
		}
		if observeErr == nil && (observed.Ready || observed.ReadyReplicas != 0 || observed.RuntimeEndpoint != "") {
			return true, w.retry(ctx, operation, "RUNTIME_STOP_PENDING", errors.New("runtime has not stopped"))
		}
		if observed.RuntimeRef != uuid.Nil {
			runtimeRef = observed.RuntimeRef
		}
		_, applyErr := w.apply(ctx, repository.Observation{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, OperationID: operation.ID,
			TargetGeneration: operation.TargetGeneration, Status: domain.StatusStopped,
			AppliedSpec: operation.TargetSpec, RuntimeRef: runtimeRef,
			RuntimeEndpoint: "", ReadyReplicas: 0, Complete: true, LeaseToken: operation.LeaseToken,
		})
		return true, applyErr
	}
	if observeErr != nil {
		return true, w.retry(ctx, operation, "RUNTIME_OBSERVE_FAILED", observeErr)
	}
	if observed.RuntimeRef == uuid.Nil {
		return true, w.retry(ctx, operation, "RUNTIME_REFERENCE_MISSING", errors.New("runtime reference is missing"))
	}
	partial := repository.Observation{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID, OperationID: operation.ID,
		TargetGeneration: operation.TargetGeneration, Status: domain.StatusDeploying,
		AppliedSpec: operation.TargetSpec, RuntimeRef: observed.RuntimeRef,
		RuntimeEndpoint: observed.RuntimeEndpoint, ReadyReplicas: observed.ReadyReplicas,
		LeaseToken: operation.LeaseToken,
	}
	if stale, err := w.apply(ctx, partial); err != nil {
		return true, err
	} else if stale {
		return true, nil
	}
	if !observed.Ready || observed.ReadyReplicas != operation.TargetSpec.Replicas {
		return true, w.retry(ctx, operation, "RUNTIME_NOT_READY", errors.New("runtime has not reached the target replicas"))
	}
	if observed.RuntimeEndpoint == "" {
		return true, w.retry(ctx, operation, "RUNTIME_ENDPOINT_MISSING", errors.New("ready runtime endpoint is missing"))
	}
	if err := w.runtime.Health(ctx, observed.RuntimeRef); err != nil {
		return true, w.retry(ctx, operation, "RUNTIME_HEALTH_FAILED", err)
	}
	if err := w.runtime.Smoke(ctx, observed.RuntimeRef, service.ServedModelName); err != nil {
		return true, w.retry(ctx, operation, "RUNTIME_SMOKE_FAILED", err)
	}
	partial.Status = domain.StatusRunning
	partial.Complete = true
	if _, err := w.apply(ctx, partial); err != nil {
		return true, err
	}
	return true, nil
}

func (w *Worker) applyRuntimeIntent(ctx context.Context, service domain.Service, operation domain.Operation, key uuid.UUID) (runtimeport.Observation, error) {
	switch operation.Type {
	case domain.ActionCreate, domain.ActionScale:
		if operation.Type == domain.ActionCreate && service.RuntimeRef != uuid.Nil {
			return w.runtime.Observe(ctx, runtimeport.RuntimeIdentity{
				TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			})
		}
		return w.runtime.Ensure(ctx, runtimeport.EnsureRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: key,
			Name: service.Name, ServedModelName: service.ServedModelName, Spec: operation.TargetSpec,
		})
	case domain.ActionStart, domain.ActionRestart:
		if service.RuntimeRef == uuid.Nil {
			return w.runtime.Ensure(ctx, runtimeport.EnsureRequest{
				TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
				Generation: operation.TargetGeneration, IdempotencyKey: key,
				Name: service.Name, ServedModelName: service.ServedModelName, Spec: operation.TargetSpec,
			})
		}
		return w.runtime.ApplyLifecycle(ctx, runtimeport.LifecycleRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: key, Action: operation.Type,
		})
	case domain.ActionStop:
		return w.runtime.ApplyLifecycle(ctx, runtimeport.LifecycleRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: key, Action: operation.Type,
		})
	default:
		return runtimeport.Observation{}, fmt.Errorf("unsupported inference operation %s", operation.Type)
	}
}

func (w *Worker) apply(ctx context.Context, observation repository.Observation) (bool, error) {
	if err := w.store.ApplyObservation(ctx, observation); err != nil {
		if errors.Is(err, repository.ErrStaleGeneration) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

func runtimeIdempotencyKey(serviceID uuid.UUID, generation int64) uuid.UUID {
	name := fmt.Sprintf("ani/inference-runtime/%s/generation/%d", serviceID, generation)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name))
}

func (w *Worker) retry(ctx context.Context, operation domain.Operation, code string, cause error) error {
	_ = cause
	retryAt := w.now().UTC().Add(w.retryDelay)
	return w.store.FailOperation(ctx, repository.Failure{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID,
		OperationID: operation.ID, TargetGeneration: operation.TargetGeneration,
		ErrorCode: code, ErrorMessage: "inference dependency is temporarily unavailable", RetryAt: &retryAt,
		LeaseToken: operation.LeaseToken,
	})
}

func (w *Worker) terminal(ctx context.Context, operation domain.Operation, code string, cause error) error {
	_ = cause
	return w.store.FailOperation(ctx, repository.Failure{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID,
		OperationID: operation.ID, TargetGeneration: operation.TargetGeneration,
		ErrorCode: code, ErrorMessage: "inference operation cannot be reconciled",
		LeaseToken: operation.LeaseToken,
	})
}
