package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	runtimeport "github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

const (
	defaultLeaseDuration   = 30 * time.Second
	defaultRetryDelay      = 5 * time.Second
	defaultMaxAttempts     = 180
	defaultDeployTimeout   = 15 * time.Minute
	requestPathEnsureGrace = 45 * time.Second
	codeRuntimeNotBound    = "RUNTIME_NOT_BOUND"
)

type Worker struct {
	store         repository.Store
	runtime       runtimeport.InferenceRuntime
	owner         string // lease 持有者，多副本时区分 worker
	now           func() time.Time
	leaseDuration time.Duration
	retryDelay    time.Duration
	maxAttempts   int           // 默认同一 operation 最多 180 次
	deployTimeout time.Duration // 默认 15 分钟后超时回收
}

func NewWorker(store repository.Store, runtime runtimeport.InferenceRuntime, owner string, now func() time.Time) *Worker {
	if now == nil {
		now = time.Now
	}
	return &Worker{
		store: store, runtime: runtime, owner: owner, now: now,
		leaseDuration: defaultLeaseDuration, retryDelay: defaultRetryDelay,
		maxAttempts: defaultMaxAttempts, deployTimeout: defaultDeployTimeout,
	}
}

func (w *Worker) WithLimits(maxAttempts int, deployTimeout, retryDelay time.Duration) *Worker {
	if maxAttempts > 0 {
		w.maxAttempts = maxAttempts
	}
	if deployTimeout > 0 {
		w.deployTimeout = deployTimeout
	}
	if retryDelay > 0 {
		w.retryDelay = retryDelay
	}
	return w
}

// Run 每秒 tick 一次。进程退出时靠 ctx 取消。
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

// RunOnce 领取一条 pending operation：Observe / Health / Smoke / 超时回收。
// 不再替用户点按钮创建 Core 资源；Create 已在请求路径 Ensure。
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
	if !generationMatches(service, operation) {
		return true, w.terminal(ctx, operation, codeStaleGeneration, repository.ErrStaleGeneration)
	}
	if service.DesiredState == domain.DesiredStateDeleted && operation.Type != domain.ActionDelete {
		return true, nil
	}
	if operation.RollbackGeneration != 0 {
		return true, w.reconcileScaleRollback(ctx, service, operation)
	}
	if w.shouldAbandon(operation) {
		return true, w.abandon(ctx, service, operation)
	}
	if w.requestPathOwnsCreate(operation, service) {
		return true, w.retry(ctx, operation, codeRuntimeNotBound, errors.New("waiting for request-path ensure"))
	}
	if operation.Type == domain.ActionDelete {
		err := w.runtime.Delete(ctx, runtimeport.DeleteRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: runtimeIdempotencyKey(operation.ServiceID, operation.TargetGeneration),
		})
		if err != nil {
			return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_DELETE_FAILED", retryable: true}, err)
		}
		_, observeErr := w.runtime.Observe(ctx, runtimeport.RuntimeIdentity{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
		})
		if observeErr == nil {
			return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_DELETE_PENDING", retryable: true}, errors.New("runtime is still observable after delete"))
		}
		if !errors.Is(observeErr, runtimeport.ErrRuntimeNotFound) {
			return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_OBSERVE_FAILED", retryable: true}, observeErr)
		}
		_, applyErr := w.apply(ctx, repository.Observation{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, OperationID: operation.ID,
			TargetGeneration: operation.TargetGeneration, Status: domain.StatusStopped,
			AppliedSpec: operation.TargetSpec, ReadyReplicas: 0, Complete: true, Deleted: true,
			LeaseToken: operation.LeaseToken,
		})
		return true, applyErr
	}

	runtimeKey := runtimeIdempotencyKey(operation.ServiceID, operation.TargetGeneration)
	accepted, err := w.applyRuntimeIntent(ctx, service, operation, runtimeKey)
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
		service.RuntimeRef = runtimeRef
	}
	if err != nil {
		return true, w.conclude(ctx, service, operation, classifyRuntimeError(err), err)
	}
	observed, observeErr := w.runtime.Observe(ctx, runtimeport.RuntimeIdentity{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: runtimeRef,
	})
	if operation.Type == domain.ActionStop {
		if observeErr != nil && !errors.Is(observeErr, runtimeport.ErrRuntimeNotFound) {
			return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_OBSERVE_FAILED", retryable: true}, observeErr)
		}
		if observeErr == nil && (observed.Ready || observed.ReadyReplicas != 0 || observed.RuntimeEndpoint != "") {
			return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_STOP_PENDING", retryable: true}, errors.New("runtime has not stopped"))
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
		return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_OBSERVE_FAILED", retryable: true}, observeErr)
	}
	if observed.RuntimeRef == uuid.Nil {
		return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_REFERENCE_MISSING", retryable: true}, errors.New("runtime reference is missing"))
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
		return true, w.conclude(ctx, service, operation, outcome{code: codeRuntimeNotReady, retryable: true}, errors.New("runtime has not reached the target replicas"))
	}
	if observed.RuntimeEndpoint == "" {
		return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_ENDPOINT_MISSING", retryable: true}, errors.New("ready runtime endpoint is missing"))
	}
	if err := w.runtime.Health(ctx, service.TenantID, observed.RuntimeRef); err != nil {
		return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_HEALTH_FAILED", retryable: true}, err)
	}
	if err := w.runtime.Smoke(ctx, service.TenantID, observed.RuntimeRef, service.ServedModelName, operation.TargetSpec.ExecutionProfile.Task); err != nil {
		return true, w.conclude(ctx, service, operation, outcome{code: "RUNTIME_SMOKE_FAILED", retryable: true}, err)
	}
	partial.Status = domain.StatusRunning
	partial.Complete = true
	if _, err := w.apply(ctx, partial); err != nil {
		return true, err
	}
	return true, nil
}

// requestPathOwnsCreate 让刚写入的 create 把第一次 Ensure 留给请求路径，避免和 worker 抢同一把 Core 幂等键。
func (w *Worker) requestPathOwnsCreate(operation domain.Operation, service domain.Service) bool {
	if operation.Type != domain.ActionCreate || service.RuntimeRef != uuid.Nil || operation.CreatedAt.IsZero() {
		return false
	}
	return w.now().UTC().Before(operation.CreatedAt.UTC().Add(requestPathEnsureGrace))
}

// applyRuntimeIntent 未绑定的 create 只在超过请求路径宽限期后 Ensure，其它动作走 Observe/Lifecycle。
func (w *Worker) applyRuntimeIntent(ctx context.Context, service domain.Service, operation domain.Operation, key uuid.UUID) (runtimeport.Observation, error) {
	switch operation.Type {
	case domain.ActionCreate:
		if service.RuntimeRef != uuid.Nil {
			return w.runtime.Observe(ctx, runtimeport.RuntimeIdentity{
				TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			})
		}
		return w.runtime.Ensure(ctx, runtimeport.EnsureRequest{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
			Generation: operation.TargetGeneration, IdempotencyKey: key,
			Name: service.Name, ServedModelName: service.ServedModelName, Spec: operation.TargetSpec,
		})
	case domain.ActionScale:
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

// conclude 可重试则排队；超时/预算耗尽则 abandon。
func (w *Worker) conclude(ctx context.Context, service domain.Service, operation domain.Operation, result outcome, cause error) error {
	if !result.retryable {
		return w.abandonWithCode(ctx, service, operation, result.code, false)
	}
	if w.timedOut(operation) {
		slog.Info("inference operation exceeded deploy timeout",
			"operation", operation.ID, "code", result.code,
			"age", w.now().UTC().Sub(operation.CreatedAt.UTC()).String(),
			"timeout", w.deployTimeout.String(),
		)
		return w.abandonWithCode(ctx, service, operation, codeDeployTimeout, false)
	}
	if result.code != codeRuntimeNotReady && w.attemptBudgetExceeded(operation) {
		return w.abandonWithCode(ctx, service, operation, result.code, true)
	}
	return w.retry(ctx, operation, result.code, cause)
}

func (w *Worker) shouldAbandon(operation domain.Operation) bool {
	if operation.ErrorCode == "" {
		return false
	}
	if !retryableCode(operation.ErrorCode) {
		return true
	}
	if w.timedOut(operation) {
		return true
	}
	return operation.ErrorCode != codeRuntimeNotReady && w.attemptBudgetExceeded(operation)
}

func (w *Worker) abandon(ctx context.Context, service domain.Service, operation domain.Operation) error {
	code := operation.ErrorCode
	deadLetter := false
	if code == "" || retryableCode(code) {
		if w.timedOut(operation) {
			code = codeDeployTimeout
		} else {
			deadLetter = operation.Attempt+1 >= w.maxAttempts
			if code == "" {
				code = codeRuntimeNotReady
			}
		}
	}
	return w.abandonWithCode(ctx, service, operation, code, deadLetter)
}

func (w *Worker) abandonWithCode(ctx context.Context, service domain.Service, operation domain.Operation, code string, deadLetter bool) error {
	if service.DesiredState == domain.DesiredStateDeleted && operation.Type != domain.ActionDelete {
		return nil
	}
	if operation.Type == domain.ActionScale {
		return w.rollbackScale(ctx, service, operation)
	}
	if operation.Type == domain.ActionCreate || operation.Type == domain.ActionStart || operation.Type == domain.ActionRestart {
		if err := w.cleanupRuntime(ctx, service, operation); err != nil {
			return w.retry(ctx, operation, code, err)
		}
	}
	if deadLetter {
		return w.deadLetter(ctx, operation, code)
	}
	return w.terminal(ctx, operation, code, errors.New(code))
}

// rollbackScale 把 replicas 打回 AppliedSpec，失败再标 ROLLBACK_FAILED。
func (w *Worker) rollbackScale(ctx context.Context, service domain.Service, operation domain.Operation) error {
	if operation.RollbackGeneration == 0 {
		generation, err := w.store.BeginScaleRollback(ctx, repository.ScaleRollback{
			TenantID: operation.TenantID, ServiceID: operation.ServiceID, OperationID: operation.ID,
			TargetGeneration: operation.TargetGeneration, LeaseToken: operation.LeaseToken,
		})
		if errors.Is(err, domain.ErrDeleted) {
			return nil
		}
		if err != nil {
			slog.Info("inference scale rollback did not begin", "operation", operation.ID, "err", err)
			return w.retry(ctx, operation, "SCALE_ROLLBACK_BEGIN_FAILED", err)
		}
		operation.RollbackGeneration = generation
		operation.UpdatedAt = w.now().UTC()
		service.Generation = generation
		service.DesiredSpec = service.AppliedSpec
		service.Status = domain.StatusDeploying
	}
	return w.reconcileScaleRollback(ctx, service, operation)
}

// reconcileScaleRollback 把副本打回 AppliedSpec，Health/Smoke 通过才算回滚成功。
func (w *Worker) reconcileScaleRollback(ctx context.Context, service domain.Service, operation domain.Operation) error {
	spec := service.DesiredSpec
	if spec.Replicas < 1 {
		spec = operation.BeforeSpec
	}
	if spec.Replicas < 1 {
		return w.finishRollback(ctx, service, operation, runtimeport.Observation{}, false)
	}
	observed, err := w.runtime.Ensure(ctx, runtimeport.EnsureRequest{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
		Generation:     operation.RollbackGeneration,
		IdempotencyKey: rollbackIdempotencyKey(operation.ID, operation.RollbackGeneration),
		Name:           service.Name, ServedModelName: service.ServedModelName, Spec: spec,
	})
	if err != nil {
		if !classifyRuntimeError(err).retryable || w.timedOut(operation) {
			return w.finishRollback(ctx, service, operation, observed, false)
		}
		return w.retry(ctx, operation, codeRuntimeMutationFailed, err)
	}
	if observed.RuntimeRef == uuid.Nil {
		observed.RuntimeRef = service.RuntimeRef
	}
	if !observed.Ready || observed.ReadyReplicas != spec.Replicas || observed.RuntimeEndpoint == "" {
		if w.timedOut(operation) {
			return w.finishRollback(ctx, service, operation, observed, false)
		}
		return w.retry(ctx, operation, codeRuntimeNotReady, errors.New("rollback runtime has not reached the previous replicas"))
	}
	if err := w.runtime.Health(ctx, service.TenantID, observed.RuntimeRef); err != nil {
		if w.timedOut(operation) {
			return w.finishRollback(ctx, service, operation, observed, false)
		}
		return w.retry(ctx, operation, "RUNTIME_HEALTH_FAILED", err)
	}
	if err := w.runtime.Smoke(ctx, service.TenantID, observed.RuntimeRef, service.ServedModelName, spec.ExecutionProfile.Task); err != nil {
		if w.timedOut(operation) {
			return w.finishRollback(ctx, service, operation, observed, false)
		}
		return w.retry(ctx, operation, "RUNTIME_SMOKE_FAILED", err)
	}
	return w.finishRollback(ctx, service, operation, observed, true)
}

func (w *Worker) finishRollback(ctx context.Context, service domain.Service, operation domain.Operation, observed runtimeport.Observation, success bool) error {
	spec := service.AppliedSpec
	if spec.Replicas < 1 {
		spec = operation.BeforeSpec
	}
	return w.store.FinishScaleRollback(ctx, repository.ScaleRollbackFinish{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID, OperationID: operation.ID,
		RollbackGeneration: operation.RollbackGeneration, AppliedSpec: spec,
		RuntimeRef: observed.RuntimeRef, RuntimeEndpoint: observed.RuntimeEndpoint,
		ReadyReplicas: observed.ReadyReplicas, LeaseToken: operation.LeaseToken, Success: success,
	})
}

// cleanupRuntime 创建失败时删掉已打出的 Core workload，避免孤儿 Deployment。
func (w *Worker) cleanupRuntime(ctx context.Context, service domain.Service, operation domain.Operation) error {
	if service.RuntimeRef == uuid.Nil {
		return nil
	}
	err := w.runtime.Delete(ctx, runtimeport.DeleteRequest{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID, RuntimeRef: service.RuntimeRef,
		Generation:     operation.TargetGeneration,
		IdempotencyKey: cleanupIdempotencyKey(operation.ServiceID, operation.TargetGeneration),
	})
	if err != nil && !errors.Is(err, runtimeport.ErrRuntimeNotFound) {
		slog.Info("inference failure cleanup did not release runtime", "operation", operation.ID, "err", err)
		return err
	}
	return nil
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
	return runtimeport.MutationIdempotencyKey(serviceID, generation)
}

func rollbackIdempotencyKey(operationID uuid.UUID, rollbackGeneration int64) uuid.UUID {
	name := fmt.Sprintf("ani/inference-runtime/%s/rollback/%d", operationID, rollbackGeneration)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name))
}

func cleanupIdempotencyKey(serviceID uuid.UUID, generation int64) uuid.UUID {
	name := fmt.Sprintf("ani/inference-runtime/%s/generation/%d/cleanup", serviceID, generation)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(name))
}

func (w *Worker) timedOut(operation domain.Operation) bool {
	start := operation.CreatedAt
	if operation.RollbackGeneration != 0 && !operation.UpdatedAt.IsZero() {
		start = operation.UpdatedAt
	}
	if start.IsZero() || w.deployTimeout <= 0 {
		return false
	}
	return !w.now().UTC().Before(start.UTC().Add(w.deployTimeout))
}

func (w *Worker) attemptBudgetExceeded(operation domain.Operation) bool {
	return operation.Attempt+1 >= w.maxAttempts
}

func codedMessage(code, text string) string {
	if code == "" {
		return text
	}
	return code + ": " + text
}

func (w *Worker) retry(ctx context.Context, operation domain.Operation, code string, cause error) error {
	if cause != nil {
		slog.Warn("inference operation retry", "operation", operation.ID, "code", code, "err", cause)
	}
	retryAt := w.now().UTC().Add(w.retryDelay)
	message := "inference dependency is temporarily unavailable"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = cause.Error()
	}
	return w.store.FailOperation(ctx, repository.Failure{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID,
		OperationID: operation.ID, TargetGeneration: operation.TargetGeneration,
		ErrorCode: code, ErrorMessage: codedMessage(code, message), RetryAt: &retryAt,
		LeaseToken: operation.LeaseToken,
	})
}

func (w *Worker) terminal(ctx context.Context, operation domain.Operation, code string, cause error) error {
	_ = cause
	return w.store.FailOperation(ctx, repository.Failure{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID,
		OperationID: operation.ID, TargetGeneration: operation.TargetGeneration,
		ErrorCode: code, ErrorMessage: codedMessage(code, "inference operation cannot be reconciled"),
		LeaseToken: operation.LeaseToken,
	})
}

func (w *Worker) deadLetter(ctx context.Context, operation domain.Operation, code string) error {
	return w.store.FailOperation(ctx, repository.Failure{
		TenantID: operation.TenantID, ServiceID: operation.ServiceID,
		OperationID: operation.ID, TargetGeneration: operation.TargetGeneration,
		ErrorCode: code, ErrorMessage: codedMessage(code, "inference operation exceeded retry budget"),
		LeaseToken: operation.LeaseToken, DeadLetter: true,
	})
}
