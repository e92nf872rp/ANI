package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

var (
	ErrModelNotFound       = errors.New("model version not found")
	ErrModelNotReady       = errors.New("model version is not ready for inference")
	ErrNoCompatibleProfile = errors.New("model version has no compatible inference profile")
)

// EngineProfile 是冻结到某次部署上的引擎底盘。Runtime 为 vllm|sglang。
// 运行镜像来自创建请求的 image_id/image_ref，不是 catalog 默认值。
type EngineProfile struct {
	ID       string
	Version  string
	Runtime  string // vllm 或 sglang，由模型能力推断，不是页面下拉
	Task     domain.InferenceTask
	ImageRef string // 仅测试替身可填；产品创建路径会覆盖为请求冻结 digest
}

// ModelVersion 是 catalog 解析后的不可变版本视图，不是 model-service 原样拷贝。
type ModelVersion struct {
	ID             uuid.UUID
	ModelID        uuid.UUID
	DisplayName    string
	Ready          bool
	Format         string // safetensors | gguf | pytorch
	SizeBytes      int64
	ArtifactRef    string // 权重路径，当前只接受 pvc://claim#/path
	ArtifactDigest string
	SecretRef      string         // 加密模型只保留 Key 引用
	EngineProfile  EngineProfile  // 兼容旧字段；实际以 CPU/GPU profile 为准
	CPUProfile     *EngineProfile // nil 表示该格式不能跑 CPU
	GPUProfile     *EngineProfile // nil 表示该格式不能跑 GPU（如 gguf）
}

// ModelCatalog 按租户解析 model_version_id，并附上可部署的引擎 profile。
type ModelCatalog interface {
	Resolve(ctx context.Context, tenantID, versionID uuid.UUID) (ModelVersion, error)
}
