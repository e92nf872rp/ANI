package reconcile

import (
	"errors"

	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	runtimeport "github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

const (
	codeAcceleratorUnavailable  = "ACCELERATOR_SPEC_UNAVAILABLE"
	codeImageUnavailable        = "IMAGE_UNAVAILABLE"
	codeEngineProfileUnapproved = "ENGINE_PROFILE_UNAPPROVED"
	codeReservedFieldConflict   = "RESERVED_FIELD_CONFLICT"
	codeModelIncompatible       = "MODEL_INCOMPATIBLE"
	codeDeployTimeout           = "DEPLOY_TIMEOUT"
	codeStaleGeneration         = "STALE_GENERATION"
	codeScaleRolledBack         = "SCALE_ROLLED_BACK"
	codeRollbackFailed          = "ROLLBACK_FAILED"
	codeRuntimeMutationFailed   = "RUNTIME_MUTATION_FAILED"
	codeRuntimeNotReady         = "RUNTIME_NOT_READY"
)

// outcome 决定这次失败是排队重试还是立刻放弃。
type outcome struct {
	code      string
	retryable bool
}

// classifyRuntimeError 把 Core 错误翻成对账错误码。容量/拓扑类不可重试。
func classifyRuntimeError(err error) outcome {
	switch {
	case errors.Is(err, runtimeport.ErrRuntimeUnsupported):
		return outcome{code: codeAcceleratorUnavailable, retryable: false}
	case errors.Is(err, runtimeport.ErrUnsupportedTopology):
		return outcome{code: "UNSUPPORTED_TOPOLOGY", retryable: false}
	case errors.Is(err, runtimeport.ErrInsufficientCapacity):
		return outcome{code: "INSUFFICIENT_CAPACITY", retryable: false}
	case errors.Is(err, runtimeport.ErrImageUnavailable):
		return outcome{code: codeImageUnavailable, retryable: false}
	case errors.Is(err, runtimeport.ErrEngineProfileUnapproved):
		return outcome{code: codeEngineProfileUnapproved, retryable: false}
	case errors.Is(err, runtimeport.ErrReservedFieldConflict):
		return outcome{code: codeReservedFieldConflict, retryable: false}
	default:
		return outcome{code: codeRuntimeMutationFailed, retryable: true}
	}
}

func retryableCode(code string) bool {
	switch code {
	case codeAcceleratorUnavailable, codeImageUnavailable, codeEngineProfileUnapproved,
		codeReservedFieldConflict, codeModelIncompatible, codeDeployTimeout,
		codeStaleGeneration, codeScaleRolledBack, codeRollbackFailed,
		"UNSUPPORTED_TOPOLOGY", "INSUFFICIENT_CAPACITY":
		return false
	default:
		return true
	}
}

// generationMatches 防止 worker 处理已被抢占的旧 generation。
func generationMatches(service domain.Service, operation domain.Operation) bool {
	if service.ActiveOperationID != operation.ID {
		return false
	}
	if operation.RollbackGeneration != 0 {
		return service.Generation == operation.RollbackGeneration
	}
	return service.Generation == operation.TargetGeneration
}
