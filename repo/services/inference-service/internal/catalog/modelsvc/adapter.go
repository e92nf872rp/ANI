package modelsvc

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var pvcClaimPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

type modelVersionAPI interface {
	GetModelVersion(context.Context, *modelv1.GetModelVersionRequest, ...grpc.CallOption) (*modelv1.GetModelVersionResponse, error)
}

// Profiles 是引擎底盘（vLLM / SGLang）。运行镜像不在这里配置。
type Profiles struct {
	CPU       catalog.EngineProfile
	GPU       catalog.EngineProfile
	SGLangCPU catalog.EngineProfile
	SGLangGPU catalog.EngineProfile
	EmbedCPU  catalog.EngineProfile
	EmbedGPU  catalog.EngineProfile
}

type Catalog struct {
	client   modelVersionAPI
	profiles Profiles
}

func DefaultProfiles() Profiles {
	return Profiles{
		CPU:       catalog.EngineProfile{ID: "vllm-chat-cpu", Version: "v1", Runtime: "vllm", Task: domain.InferenceTaskGenerate},
		GPU:       catalog.EngineProfile{ID: "vllm-chat-gpu", Version: "v1", Runtime: "vllm", Task: domain.InferenceTaskGenerate},
		SGLangCPU: catalog.EngineProfile{ID: "sglang-chat-cpu", Version: "v1", Runtime: "sglang", Task: domain.InferenceTaskGenerate},
		SGLangGPU: catalog.EngineProfile{ID: "sglang-chat-gpu", Version: "v1", Runtime: "sglang", Task: domain.InferenceTaskGenerate},
		EmbedCPU:  catalog.EngineProfile{ID: "vllm-embed-cpu", Version: "v1", Runtime: "vllm", Task: domain.InferenceTaskEmbed},
		EmbedGPU:  catalog.EngineProfile{ID: "vllm-embed-gpu", Version: "v1", Runtime: "vllm", Task: domain.InferenceTaskEmbed},
	}
}

func New(client modelVersionAPI, profiles Profiles) (*Catalog, error) {
	if client == nil {
		return nil, fmt.Errorf("model-service client is required")
	}
	if err := profiles.validate(); err != nil {
		return nil, err
	}
	return &Catalog{client: client, profiles: profiles}, nil
}

func Dial(addr string) (*Catalog, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("model-service gRPC address is empty")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial model-service %s: %w", addr, err)
	}
	return New(modelv1.NewModelServiceClient(conn), DefaultProfiles())
}

// Resolve 按租户取不可变模型版本，冻结可部署的 vLLM/SGLang profile。
// 当前只认 pvc://claim#/path；HF/MinIO 还没有兼容 profile。
func (c *Catalog) Resolve(ctx context.Context, tenantID, versionID uuid.UUID) (catalog.ModelVersion, error) {
	if tenantID == uuid.Nil || versionID == uuid.Nil {
		return catalog.ModelVersion{}, catalog.ErrModelNotFound
	}
	resp, err := c.client.GetModelVersion(ctx, &modelv1.GetModelVersionRequest{
		TenantId:       tenantID.String(),
		ModelVersionId: versionID.String(),
	})
	if err != nil {
		return catalog.ModelVersion{}, mapLookupError(err)
	}
	if resp == nil || resp.GetModel() == nil || resp.GetVersion() == nil {
		return catalog.ModelVersion{}, catalog.ErrModelNotFound
	}
	model := resp.GetModel()
	version := resp.GetVersion()
	if strings.TrimSpace(model.GetTenantId()) != tenantID.String() {
		return catalog.ModelVersion{}, catalog.ErrModelNotFound
	}
	parsedVersionID, err := uuid.Parse(strings.TrimSpace(version.GetId()))
	if err != nil || parsedVersionID != versionID {
		return catalog.ModelVersion{}, catalog.ErrModelNotFound
	}
	modelID, err := uuid.Parse(strings.TrimSpace(version.GetModelId()))
	if err != nil || modelID == uuid.Nil {
		return catalog.ModelVersion{}, catalog.ErrModelNotFound
	}
	task, supported := inferenceTask(model.GetCapabilities())
	if !supported {
		return catalog.ModelVersion{}, catalog.ErrNoCompatibleProfile
	}

	out := catalog.ModelVersion{
		ID:             parsedVersionID,
		ModelID:        modelID,
		DisplayName:    displayName(model, version),
		Ready:          strings.EqualFold(strings.TrimSpace(model.GetStatus()), "ready"),
		Format:         strings.TrimSpace(version.GetFormat()),
		SizeBytes:      version.GetSizeBytes(),
		ArtifactRef:    strings.TrimSpace(version.GetStoragePath()),
		ArtifactDigest: normalizeDigest(version.GetChecksumSha256()),
	}
	if !localPVCArtifact(out.ArtifactRef) {
		return catalog.ModelVersion{}, catalog.ErrNoCompatibleProfile
	}
	if version.GetIsEncrypted() {
		out.SecretRef = "model-encrypt/" + parsedVersionID.String()
	}
	cpuProfile, gpuProfile := c.profiles.CPU, c.profiles.GPU
	if task == domain.InferenceTaskEmbed {
		cpuProfile, gpuProfile = c.profiles.EmbedCPU, c.profiles.EmbedGPU
	} else if prefersSGLang(model.GetCapabilities()) {
		cpuProfile, gpuProfile = c.profiles.SGLangCPU, c.profiles.SGLangGPU
	}
	if cpuCompatible(out.Format) {
		out.CPUProfile = cloneProfile(cpuProfile)
	}
	if gpuCompatible(out.Format) {
		out.GPUProfile = cloneProfile(gpuProfile)
	}
	if out.CPUProfile == nil && out.GPUProfile == nil {
		return catalog.ModelVersion{}, catalog.ErrNoCompatibleProfile
	}
	return out, nil
}

func (p Profiles) validate() error {
	for _, item := range []struct {
		kind            string
		profile         catalog.EngineProfile
		expectedTask    domain.InferenceTask
		expectedRuntime string
	}{
		{"cpu", p.CPU, domain.InferenceTaskGenerate, ""},
		{"gpu", p.GPU, domain.InferenceTaskGenerate, ""},
		{"sglang-cpu", p.SGLangCPU, domain.InferenceTaskGenerate, ""},
		{"sglang-gpu", p.SGLangGPU, domain.InferenceTaskGenerate, ""},
		{"embed-cpu", p.EmbedCPU, domain.InferenceTaskEmbed, "vllm"},
		{"embed-gpu", p.EmbedGPU, domain.InferenceTaskEmbed, "vllm"},
	} {
		if err := validateProfile(item.kind, item.profile, item.expectedTask, item.expectedRuntime); err != nil {
			return err
		}
	}
	return nil
}

func validateProfile(kind string, profile catalog.EngineProfile, expectedTask domain.InferenceTask, expectedRuntime string) error {
	switch {
	case strings.TrimSpace(profile.ID) == "":
		return fmt.Errorf("%s engine profile id is required", kind)
	case strings.TrimSpace(profile.Version) == "":
		return fmt.Errorf("%s engine profile version is required", kind)
	case strings.TrimSpace(profile.Runtime) == "":
		return fmt.Errorf("%s engine profile runtime is required", kind)
	case profile.Task != expectedTask:
		return fmt.Errorf("%s engine profile task must be %q", kind, expectedTask)
	case expectedRuntime != "" && strings.ToLower(strings.TrimSpace(profile.Runtime)) != expectedRuntime:
		return fmt.Errorf("%s engine profile runtime must be %q", kind, expectedRuntime)
	default:
		return nil
	}
}

func mapLookupError(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return catalog.ErrModelNotFound
	case codes.InvalidArgument:
		return catalog.ErrModelNotFound
	default:
		return err
	}
}

func inferenceTask(capabilities []string) (domain.InferenceTask, bool) {
	if len(capabilities) == 0 {
		return domain.InferenceTaskGenerate, true
	}
	hasEmbedding := false
	for _, capability := range capabilities {
		switch strings.ToLower(strings.TrimSpace(capability)) {
		case "text-generation", "sglang":
			return domain.InferenceTaskGenerate, true
		case "embedding":
			hasEmbedding = true
		}
	}
	if hasEmbedding {
		return domain.InferenceTaskEmbed, true
	}
	return "", false
}

// prefersSGLang 是过渡旁路：OpenAPI 还没有 engine_runtime，capability 带 sglang 才选 SGLang。
func prefersSGLang(capabilities []string) bool {
	for _, capability := range capabilities {
		if strings.EqualFold(strings.TrimSpace(capability), "sglang") {
			return true
		}
	}
	return false
}

// localPVCArtifact 只接受 pvc://<dns-label>#/path，拒绝 object:// 和 HostPath。
func localPVCArtifact(ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "..") {
		return false
	}
	rest, ok := strings.CutPrefix(ref, "pvc://")
	if !ok {
		return false
	}
	claim, _, _ := strings.Cut(rest, "#")
	return pvcClaimPattern.MatchString(strings.TrimSpace(claim))
}

func cpuCompatible(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "safetensors", "gguf", "pytorch":
		return true
	default:
		return false
	}
}

func gpuCompatible(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "safetensors", "pytorch":
		return true
	default:
		return false
	}
}

func displayName(model *modelv1.Model, version *modelv1.ModelVersion) string {
	name := strings.TrimSpace(model.GetDisplayName())
	if name == "" {
		name = strings.TrimSpace(model.GetName())
	}
	if ver := strings.TrimSpace(version.GetVersion()); ver != "" && name != "" {
		return name + " / " + ver
	}
	return name
}

func normalizeDigest(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "sha256:") {
		return raw
	}
	return "sha256:" + raw
}

func cloneProfile(profile catalog.EngineProfile) *catalog.EngineProfile {
	cloned := profile
	return &cloned
}

var _ catalog.ModelCatalog = (*Catalog)(nil)
