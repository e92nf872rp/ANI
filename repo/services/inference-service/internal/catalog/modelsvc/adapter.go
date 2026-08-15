package modelsvc

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var digestPinnedImage = regexp.MustCompile(`^.+@sha256:[a-f0-9]{64}$`)

const (
	defaultCPUImage = "registry.ani.internal/platform/vllm-openai-cpu@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	defaultGPUImage = "registry.ani.internal/platform/vllm-openai-gpu@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type modelVersionAPI interface {
	GetModelVersion(context.Context, *modelv1.GetModelVersionRequest, ...grpc.CallOption) (*modelv1.GetModelVersionResponse, error)
}

type Profiles struct {
	CPU catalog.EngineProfile
	GPU catalog.EngineProfile
}

type Catalog struct {
	client   modelVersionAPI
	profiles Profiles
}

func DefaultProfiles() Profiles {
	return Profiles{
		CPU: catalog.EngineProfile{ID: "vllm-chat-cpu", Version: "v1", Runtime: "vllm", ImageRef: defaultCPUImage},
		GPU: catalog.EngineProfile{ID: "vllm-chat-gpu", Version: "v1", Runtime: "vllm", ImageRef: defaultGPUImage},
	}
}

func ProfilesFromImages(cpuImage, gpuImage string) Profiles {
	profiles := DefaultProfiles()
	if image := strings.TrimSpace(cpuImage); image != "" {
		profiles.CPU.ImageRef = image
	}
	if image := strings.TrimSpace(gpuImage); image != "" {
		profiles.GPU.ImageRef = image
	}
	return profiles
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

func Dial(addr string, profiles Profiles) (*Catalog, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, fmt.Errorf("model-service gRPC address is empty")
	}
	if err := profiles.validate(); err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial model-service %s: %w", addr, err)
	}
	return New(modelv1.NewModelServiceClient(conn), profiles)
}

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
	if !supportsChat(model.GetCapabilities()) {
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
	if version.GetIsEncrypted() {
		out.SecretRef = "model-encrypt/" + parsedVersionID.String()
	}
	if cpuCompatible(out.Format) {
		out.CPUProfile = cloneProfile(c.profiles.CPU)
	}
	if gpuCompatible(out.Format) {
		out.GPUProfile = cloneProfile(c.profiles.GPU)
	}
	if out.CPUProfile == nil && out.GPUProfile == nil {
		return catalog.ModelVersion{}, catalog.ErrNoCompatibleProfile
	}
	return out, nil
}

func (p Profiles) validate() error {
	if err := validateProfile("cpu", p.CPU); err != nil {
		return err
	}
	return validateProfile("gpu", p.GPU)
}

func validateProfile(kind string, profile catalog.EngineProfile) error {
	switch {
	case strings.TrimSpace(profile.ID) == "":
		return fmt.Errorf("%s engine profile id is required", kind)
	case strings.TrimSpace(profile.Version) == "":
		return fmt.Errorf("%s engine profile version is required", kind)
	case strings.TrimSpace(profile.Runtime) == "":
		return fmt.Errorf("%s engine profile runtime is required", kind)
	case !digestPinnedImage.MatchString(strings.TrimSpace(profile.ImageRef)):
		return fmt.Errorf("%s engine profile image must be digest-pinned", kind)
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

func supportsChat(capabilities []string) bool {
	if len(capabilities) == 0 {
		return true
	}
	for _, capability := range capabilities {
		if strings.EqualFold(strings.TrimSpace(capability), "text-generation") {
			return true
		}
	}
	return false
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
