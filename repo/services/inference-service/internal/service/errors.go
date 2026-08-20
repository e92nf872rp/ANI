package service

import "errors"

var (
	ErrInvalidInput               = errors.New("invalid inference request")                      // 请求字段不合法
	ErrUnsupportedTopology        = errors.New("unsupported inference topology")                 // CPU 多节点 / LWS 不可用
	ErrAcceleratorSpecUnavailable = errors.New("accelerator spec is not available")              // GPU spec 不在 Core 能力清单
	ErrInsufficientCapacity       = errors.New("inference capacity is insufficient")             // 单节点 GPU 数不够
	ErrImageUnavailable           = errors.New("inference runtime image is unavailable")         // 批准镜像不可用
	ErrEngineProfileUnapproved    = errors.New("inference engine profile is not approved")       // 引擎底盘未批准
	ErrReservedFieldConflict      = errors.New("inference reserved field conflicts")             // Core 保留字段冲突
	ErrRuntimeIntentConflict      = errors.New("inference runtime idempotency intent conflicts") // 同一幂等键意图不同
)
