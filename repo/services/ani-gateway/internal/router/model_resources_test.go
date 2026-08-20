package router

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeModelClient struct {
	lastTenantID string
	lastModelID  string
	lastCreate   *modelv1.CreateModelRequest
	lastVersion  *modelv1.CreateModelVersionRequest
	listResp     *modelv1.ListModelsResponse
	createResp   *modelv1.Model
	getResp      *modelv1.Model
	versionResp  *modelv1.ModelVersion
	err          error
}

func (f *fakeModelClient) ListModels(_ context.Context, tenantID, _ string, _ int32, _ string) (*modelv1.ListModelsResponse, error) {
	f.lastTenantID = tenantID
	return f.listResp, f.err
}
func (f *fakeModelClient) CreateModel(_ context.Context, tenantID string, req *modelv1.CreateModelRequest) (*modelv1.Model, error) {
	f.lastTenantID = tenantID
	f.lastCreate = req
	return f.createResp, f.err
}
func (f *fakeModelClient) GetModel(_ context.Context, tenantID, modelID string) (*modelv1.Model, error) {
	f.lastTenantID, f.lastModelID = tenantID, modelID
	return f.getResp, f.err
}
func (f *fakeModelClient) DeleteModel(_ context.Context, tenantID, modelID string) (*emptypb.Empty, error) {
	f.lastTenantID, f.lastModelID = tenantID, modelID
	return &emptypb.Empty{}, f.err
}
func (f *fakeModelClient) CreateModelVersion(_ context.Context, tenantID string, req *modelv1.CreateModelVersionRequest) (*modelv1.ModelVersion, error) {
	f.lastTenantID = tenantID
	f.lastVersion = req
	return f.versionResp, f.err
}

func setupModelTestServer(t *testing.T, client ModelServiceClient) *server.Hertz {
	t.Helper()
	prev := modelServiceClient
	modelServiceClient = client
	t.Cleanup(func() { modelServiceClient = prev })
	h := server.Default()
	h.Use(middleware.RequestID())
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		if tenantID := string(c.GetHeader("X-Dev-Tenant-ID")); tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
		c.Next(ctx)
	})
	registerModels(h.Group("/api/v1/svc"))
	return h
}

func performModel(h *server.Hertz, method, path, body, tenant string) *protocol.Response {
	var bodyArg *ut.Body
	if body != "" {
		bodyArg = &ut.Body{Body: strings.NewReader(body), Len: len(body)}
	}
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
	if tenant != "" {
		headers = append(headers, ut.Header{Key: "X-Dev-Tenant-ID", Value: tenant})
	}
	return ut.PerformRequest(h.Engine, method, path, bodyArg, headers...).Result()
}

func sampleModel() *modelv1.Model {
	return &modelv1.Model{
		Id: "22222222-2222-2222-2222-222222222222", Name: "qwen", DisplayName: "Qwen 7B",
		Source: "upload", Capabilities: []string{"text-generation"}, Status: "ready",
		CreatedAt: timestamppb.New(time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC)),
		Versions:  []*modelv1.ModelVersion{sampleModelVersion()},
	}
}

func sampleModelVersion() *modelv1.ModelVersion {
	return &modelv1.ModelVersion{
		Id: "33333333-3333-3333-3333-333333333333", ModelId: "22222222-2222-2222-2222-222222222222",
		Version: "v1", Format: "safetensors", StoragePath: "pvc://vllm-model#/models/qwen",
		ChecksumSha256: "abc", SizeBytes: 12,
		CreatedAt: timestamppb.New(time.Date(2026, 8, 17, 1, 2, 3, 0, time.UTC)),
	}
}

func TestModelRoutesRequireClient(t *testing.T) {
	h := setupModelTestServer(t, nil)
	resp := performModel(h, http.MethodGet, "/api/v1/svc/models", "", "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestCreateModelAndLocalPVCVersion(t *testing.T) {
	client := &fakeModelClient{createResp: sampleModel(), versionResp: sampleModelVersion(), getResp: sampleModel()}
	h := setupModelTestServer(t, client)
	tenant := "11111111-1111-1111-1111-111111111111"

	created := performModel(h, http.MethodPost, "/api/v1/svc/models", `{"idempotency_key":"44444444-4444-4444-4444-444444444444","name":"qwen","display_name":"Qwen 7B","capabilities":["text-generation"]}`, tenant)
	if created.StatusCode() != http.StatusCreated {
		t.Fatalf("create model status = %d body=%s", created.StatusCode(), created.Body())
	}
	if client.lastTenantID != tenant || client.lastCreate.GetName() != "qwen" {
		t.Fatalf("create forwarded %+v tenant=%s", client.lastCreate, client.lastTenantID)
	}

	version := performModel(h, http.MethodPost, "/api/v1/svc/models/22222222-2222-2222-2222-222222222222/versions", `{"idempotency_key":"55555555-5555-5555-5555-555555555555","version":"v1","format":"safetensors","storage_path":"pvc://vllm-model#/models/qwen","checksum_sha256":"abc","size_bytes":12}`, tenant)
	if version.StatusCode() != http.StatusCreated {
		t.Fatalf("create version status = %d body=%s", version.StatusCode(), version.Body())
	}
	if client.lastVersion.GetStoragePath() != "pvc://vllm-model#/models/qwen" {
		t.Fatalf("storage_path = %q", client.lastVersion.GetStoragePath())
	}

	listed := performModel(h, http.MethodGet, "/api/v1/svc/models/22222222-2222-2222-2222-222222222222/versions", "", tenant)
	if listed.StatusCode() != http.StatusOK {
		t.Fatalf("list versions status = %d body=%s", listed.StatusCode(), listed.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(listed.Body(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %#v", body["items"])
	}
}

func TestCreateModelVersionRejectsObjectStorePath(t *testing.T) {
	h := setupModelTestServer(t, &fakeModelClient{versionResp: sampleModelVersion()})
	resp := performModel(h, http.MethodPost, "/api/v1/svc/models/22222222-2222-2222-2222-222222222222/versions", `{"idempotency_key":"55555555-5555-5555-5555-555555555555","version":"v1","format":"safetensors","storage_path":"object://models/qwen/v1","checksum_sha256":"abc","size_bytes":12}`, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestImportModelReturnsNotImplemented(t *testing.T) {
	h := setupModelTestServer(t, &fakeModelClient{})
	resp := performModel(h, http.MethodPost, "/api/v1/svc/models/import", `{"source":"huggingface","repo_id":"Qwen/Qwen2.5-7B-Instruct","idempotency_key":"k1"}`, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusNotImplemented {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	var body map[string]any
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "FEATURE_NOT_AVAILABLE" {
		t.Fatalf("body = %#v", body)
	}
}

func TestGetModelMapsNotFound(t *testing.T) {
	h := setupModelTestServer(t, &fakeModelClient{err: status.Error(codes.NotFound, "not found")})
	resp := performModel(h, http.MethodGet, "/api/v1/svc/models/22222222-2222-2222-2222-222222222222", "", "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
}
