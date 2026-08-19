package modelsvc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stubClient struct {
	resp *modelv1.GetModelVersionResponse
	err  error
	req  *modelv1.GetModelVersionRequest
}

func (s *stubClient) GetModelVersion(_ context.Context, in *modelv1.GetModelVersionRequest, _ ...grpc.CallOption) (*modelv1.GetModelVersionResponse, error) {
	s.req = in
	return s.resp, s.err
}

func TestResolveReadySafetensorsAssignsCPUAndGPUProfiles(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	client := &stubClient{resp: readyResponse(tenantID, modelID, versionID, "safetensors", "ready", []string{"text-generation"}, false, "")}
	cat := mustCatalog(t, client)

	got, err := cat.Resolve(context.Background(), tenantID, versionID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.Ready || got.CPUProfile == nil || got.GPUProfile == nil {
		t.Fatalf("resolved = %+v", got)
	}
	if got.DisplayName != "Qwen 7B / v1" || got.ArtifactDigest != "sha256:abc" {
		t.Fatalf("snapshot = %+v", got)
	}
	if got.CPUProfile.ID != "vllm-chat-cpu" || got.CPUProfile.Runtime != "vllm" || got.GPUProfile.ID != "vllm-chat-gpu" {
		t.Fatalf("profiles = cpu=%+v gpu=%+v", got.CPUProfile, got.GPUProfile)
	}
	if got.SecretRef != "" {
		t.Fatalf("unexpected secret ref %q", got.SecretRef)
	}
}

func TestResolveNotFound(t *testing.T) {
	cat := mustCatalog(t, &stubClient{err: status.Error(codes.NotFound, "not found")})
	_, err := cat.Resolve(context.Background(), uuid.MustParse("11111111-1111-1111-1111-111111111111"), uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	if !errors.Is(err, catalog.ErrModelNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveNotReadyKeepsProfiles(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	cat := mustCatalog(t, &stubClient{resp: readyResponse(tenantID, modelID, versionID, "safetensors", "pending", nil, false, "")})

	got, err := cat.Resolve(context.Background(), tenantID, versionID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Ready || got.CPUProfile == nil {
		t.Fatalf("resolved = %+v", got)
	}
}

func TestResolveGGUFHasCPUOnly(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	cat := mustCatalog(t, &stubClient{resp: readyResponse(tenantID, modelID, versionID, "gguf", "ready", nil, false, "")})

	got, err := cat.Resolve(context.Background(), tenantID, versionID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.CPUProfile == nil || got.GPUProfile != nil {
		t.Fatalf("resolved = %+v", got)
	}
}

func TestResolveUnknownFormatIsIncompatible(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	cat := mustCatalog(t, &stubClient{resp: readyResponse(tenantID, modelID, versionID, "onnx", "ready", nil, false, "")})

	_, err := cat.Resolve(context.Background(), tenantID, versionID)
	if !errors.Is(err, catalog.ErrNoCompatibleProfile) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveEmbeddingOnlyIsIncompatible(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	cat := mustCatalog(t, &stubClient{resp: readyResponse(tenantID, modelID, versionID, "safetensors", "ready", []string{"embedding"}, false, "")})

	_, err := cat.Resolve(context.Background(), tenantID, versionID)
	if !errors.Is(err, catalog.ErrNoCompatibleProfile) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveEncryptedUsesKeyRefNotHint(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	resp := readyResponse(tenantID, modelID, versionID, "safetensors", "ready", nil, true, "password-hint")
	cat := mustCatalog(t, &stubClient{resp: resp})

	got, err := cat.Resolve(context.Background(), tenantID, versionID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.SecretRef != "model-encrypt/"+versionID.String() {
		t.Fatalf("secret ref = %q", got.SecretRef)
	}
	if strings.Contains(got.SecretRef, "password-hint") || strings.Contains(got.ArtifactRef, "password-hint") {
		t.Fatalf("hint leaked: %+v", got)
	}
}

func TestResolveUnavailableIsNotMappedToNotFound(t *testing.T) {
	unavailable := status.Error(codes.Unavailable, "model-service down")
	cat := mustCatalog(t, &stubClient{err: unavailable})
	_, err := cat.Resolve(context.Background(), uuid.MustParse("11111111-1111-1111-1111-111111111111"), uuid.MustParse("33333333-3333-3333-3333-333333333333"))
	if !errors.Is(err, unavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveObjectStoreArtifactIsIncompatible(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	resp := readyResponse(tenantID, modelID, versionID, "safetensors", "ready", []string{"text-generation"}, false, "")
	resp.Version.StoragePath = "object://models/qwen/v1"
	cat := mustCatalog(t, &stubClient{resp: resp})

	_, err := cat.Resolve(context.Background(), tenantID, versionID)
	if !errors.Is(err, catalog.ErrNoCompatibleProfile) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveSGLangCapabilitySelectsSGLangRuntime(t *testing.T) {
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	versionID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	modelID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	cat := mustCatalog(t, &stubClient{resp: readyResponse(tenantID, modelID, versionID, "safetensors", "ready", []string{"text-generation", "sglang"}, false, "")})

	got, err := cat.Resolve(context.Background(), tenantID, versionID)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.CPUProfile == nil || got.CPUProfile.Runtime != "sglang" || got.GPUProfile.Runtime != "sglang" {
		t.Fatalf("profiles = cpu=%+v gpu=%+v", got.CPUProfile, got.GPUProfile)
	}
	if got.CPUProfile.ID != "sglang-chat-cpu" {
		t.Fatalf("cpu profile id = %q", got.CPUProfile.ID)
	}
}

func TestNewRejectsEmptyRuntime(t *testing.T) {
	profiles := DefaultProfiles()
	profiles.CPU.Runtime = ""
	_, err := New(&stubClient{}, profiles)
	if err == nil {
		t.Fatal("expected engine profile runtime error")
	}
}

func mustCatalog(t *testing.T, client modelVersionAPI) *Catalog {
	t.Helper()
	cat, err := New(client, DefaultProfiles())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return cat
}

func readyResponse(tenantID, modelID, versionID uuid.UUID, format, status string, capabilities []string, encrypted bool, hint string) *modelv1.GetModelVersionResponse {
	return &modelv1.GetModelVersionResponse{
		Model: &modelv1.Model{
			TenantId:     tenantID.String(),
			Id:           modelID.String(),
			Name:         "qwen",
			DisplayName:  "Qwen 7B",
			Capabilities: capabilities,
			Status:       status,
		},
		Version: &modelv1.ModelVersion{
			Id:             versionID.String(),
			ModelId:        modelID.String(),
			Version:        "v1",
			Format:         format,
			IsEncrypted:    encrypted,
			EncryptAlgo:    "sm4",
			EncryptHint:    hint,
			SizeBytes:      12,
			ChecksumSha256: "abc",
			StoragePath:    "pvc://vllm-model#/models/qwen",
		},
	}
}
