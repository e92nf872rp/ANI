package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

// mapRuntimeError 把 Core runtime 错误翻成产品层错误码，供 Gateway 映射 HTTP。
func mapRuntimeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, runtime.ErrRuntimeUnsupported):
		return ErrAcceleratorSpecUnavailable
	case errors.Is(err, runtime.ErrUnsupportedTopology):
		return ErrUnsupportedTopology
	case errors.Is(err, runtime.ErrInsufficientCapacity):
		return ErrInsufficientCapacity
	case errors.Is(err, runtime.ErrImageUnavailable):
		return ErrImageUnavailable
	case errors.Is(err, runtime.ErrEngineProfileUnapproved):
		return ErrEngineProfileUnapproved
	case errors.Is(err, runtime.ErrReservedFieldConflict):
		return ErrReservedFieldConflict
	case errors.Is(err, runtime.ErrRuntimeIntentConflict):
		return ErrRuntimeIntentConflict
	default:
		return err
	}
}

// dispatchRuntime 请求路径同步打 Core。幂等键必须与 worker 共用 MutationIdempotencyKey。
func dispatchRuntime(ctx context.Context, rt runtime.InferenceRuntime, service domain.Service, operation domain.Operation) (runtime.Observation, error) {
	if rt == nil {
		return runtime.Observation{}, nil
	}
	key := runtime.MutationIdempotencyKey(operation.ServiceID, operation.TargetGeneration)
	switch operation.Type {
	case domain.ActionCreate, domain.ActionScale:
		if operation.Type == domain.ActionCreate && service.RuntimeRef != uuid.Nil {
			return runtime.Observation{RuntimeRef: service.RuntimeRef}, nil
		}
		return rt.Ensure(ctx, runtime.EnsureRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: key,
			Name: service.Name, ServedModelName: service.ServedModelName, Spec: operation.TargetSpec,
		})
	case domain.ActionStart, domain.ActionRestart:
		if service.RuntimeRef == uuid.Nil {
			return rt.Ensure(ctx, runtime.EnsureRequest{
				TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
				Generation: operation.TargetGeneration, IdempotencyKey: key,
				Name: service.Name, ServedModelName: service.ServedModelName, Spec: operation.TargetSpec,
			})
		}
		return rt.ApplyLifecycle(ctx, runtime.LifecycleRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: key, Action: operation.Type,
		})
	case domain.ActionStop:
		return rt.ApplyLifecycle(ctx, runtime.LifecycleRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: key, Action: operation.Type,
		})
	case domain.ActionDelete:
		return runtime.Observation{}, rt.Delete(ctx, runtime.DeleteRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: key,
		})
	default:
		return runtime.Observation{}, nil
	}
}

// bindRuntime 把 Core 返回的 platform-workload ID 写回 inference 行。
func bindRuntime(ctx context.Context, store interface {
	BindRuntimeRef(context.Context, repository.RuntimeBinding) error
}, service domain.Service, operation domain.Operation, runtimeRef uuid.UUID) error {
	if runtimeRef == uuid.Nil {
		return nil
	}
	return store.BindRuntimeRef(ctx, repository.RuntimeBinding{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID, OperationID: operation.ID,
		Generation: operation.TargetGeneration, RuntimeRef: runtimeRef,
	})
}
