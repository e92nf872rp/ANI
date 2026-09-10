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
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type fakeModelClient struct {
	lastTenantID    string
	lastModelID     string
	lastCreate      *modelv1.CreateModelRequest
	lastVersion     *modelv1.CreateModelVersionRequest
	lastUpload      *modelv1.GetUploadURLRequest
	lastImport      *modelv1.ImportModelRequest
	listResp        *modelv1.ListModelsResponse
	createResp      *modelv1.Model
	getResp         *modelv1.Model
	versionResp     *modelv1.ModelVersion
	versionListResp *modelv1.ListModelVersionsResponse
	uploadResp      *modelv1.GetUploadURLResponse
	importResp      *commonv1.AsyncTaskRef
	listStatus      string
	listSource      string
	listCapability  string
	listKeyword     string
	err             error
}

func (f *fakeModelClient) ListModels(_ context.Context, tenantID, status, source, capability, keyword string, _ int32, _ string) (*modelv1.ListModelsResponse, error) {
	f.lastTenantID = tenantID
	f.listStatus, f.listSource, f.listCapability, f.listKeyword = status, source, capability, keyword
	return f.listResp, f.err
}
func (f *fakeModelClient) ListModelVersions(_ context.Context, tenantID, modelID string, _ int32, _ string) (*modelv1.ListModelVersionsResponse, error) {
	f.lastTenantID, f.lastModelID = tenantID, modelID
	return f.versionListResp, f.err
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
func (f *fakeModelClient) GetUploadURL(_ context.Context, tenantID string, req *modelv1.GetUploadURLRequest) (*modelv1.GetUploadURLResponse, error) {
	f.lastTenantID = tenantID
	f.lastUpload = req
	return f.uploadResp, f.err
}
func (f *fakeModelClient) ImportModel(_ context.Context, tenantID string, req *modelv1.ImportModelRequest) (*commonv1.AsyncTaskRef, error) {
	f.lastTenantID = tenantID
	f.lastImport = req
	return f.importResp, f.err
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
	client := &fakeModelClient{createResp: sampleModel(), versionResp: sampleModelVersion(), getResp: sampleModel(), versionListResp: &modelv1.ListModelVersionsResponse{Versions: []*modelv1.ModelVersion{sampleModelVersion()}}}
	h := setupModelTestServer(t, client)
	tenant := "11111111-1111-1111-1111-111111111111"

	created := performModel(h, http.MethodPost, "/api/v1/svc/models", `{"idempotency_key":"44444444-4444-4444-4444-444444444444","name":"qwen","display_name":"Qwen 7B","capabilities":["text-generation"]}`, tenant)
	if created.StatusCode() != http.StatusCreated {
		t.Fatalf("create model status = %d body=%s", created.StatusCode(), created.Body())
	}
	if client.lastTenantID != tenant || client.lastCreate.GetName() != "qwen" {
		t.Fatalf("create forwarded %+v tenant=%s", client.lastCreate, client.lastTenantID)
	}
	if client.lastCreate.GetIdempotencyKey() == "" {
		t.Fatal("create idempotency key was not forwarded")
	}

	version := performModel(h, http.MethodPost, "/api/v1/svc/models/22222222-2222-2222-2222-222222222222/versions", `{"idempotency_key":"55555555-5555-5555-5555-555555555555","version":"v1","format":"safetensors","storage_path":"pvc://vllm-model#/models/qwen","checksum_sha256":"abc","size_bytes":12}`, tenant)
	if version.StatusCode() != http.StatusCreated {
		t.Fatalf("create version status = %d body=%s", version.StatusCode(), version.Body())
	}
	if client.lastVersion.GetStoragePath() != "pvc://vllm-model#/models/qwen" {
		t.Fatalf("storage_path = %q", client.lastVersion.GetStoragePath())
	}
	if client.lastVersion.GetIdempotencyKey() == "" {
		t.Fatal("version idempotency key was not forwarded")
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

func TestModelListFiltersAndUploadURLRoute(t *testing.T) {
	client := &fakeModelClient{
		listResp:   &modelv1.ListModelsResponse{},
		uploadResp: &modelv1.GetUploadURLResponse{UploadUrl: "https://object.invalid/put", StoragePath: "object://models/tenant/model/v1/doc/file", DocId: "doc", UploadHeaders: map[string]string{"x-amz-meta-sha256": "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}},
	}
	h := setupModelTestServer(t, client)
	tenant := "11111111-1111-1111-1111-111111111111"
	resp := performModel(h, http.MethodGet, "/api/v1/svc/models?status=ready&source=upload&capability=embedding&keyword=qwen&limit=10", "", tenant)
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("list status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.listStatus != "ready" || client.listSource != "upload" || client.listCapability != "embedding" || client.listKeyword != "qwen" {
		t.Fatalf("filters = %q/%q/%q/%q", client.listStatus, client.listSource, client.listCapability, client.listKeyword)
	}
	resp = performModel(h, http.MethodPost, "/api/v1/svc/models/22222222-2222-2222-2222-222222222222/upload-url", `{"idempotency_key":"upload-1","version":"v1","file_name":"model.safetensors","size_bytes":10,"checksum_sha256":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}`, tenant)
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("upload URL status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastUpload == nil || client.lastUpload.GetIdempotencyKey() != "upload-1" || client.lastUpload.GetModelId() == "" {
		t.Fatalf("upload request = %+v", client.lastUpload)
	}
	if client.lastUpload.GetChecksumSha256() == "" {
		t.Fatal("upload checksum was not forwarded")
	}
	var uploadBody map[string]any
	if err := json.Unmarshal(resp.Body(), &uploadBody); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	headers, _ := uploadBody["upload_headers"].(map[string]any)
	if headers["x-amz-meta-sha256"] != "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" {
		t.Fatalf("upload_headers = %#v", uploadBody["upload_headers"])
	}
}

func TestCreateModelVersionRejectsObjectStorePath(t *testing.T) {
	h := setupModelTestServer(t, &fakeModelClient{versionResp: sampleModelVersion()})
	resp := performModel(h, http.MethodPost, "/api/v1/svc/models/22222222-2222-2222-2222-222222222222/versions", `{"idempotency_key":"55555555-5555-5555-5555-555555555555","version":"v1","format":"safetensors","storage_path":"object://models/qwen/v1","checksum_sha256":"abc","size_bytes":12}`, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestImportModelHuggingFaceReturnsAcceptedAndLocation(t *testing.T) {
	client := &fakeModelClient{importResp: &commonv1.AsyncTaskRef{TaskId: "task-hf"}}
	h := setupModelTestServer(t, client)
	tenant := "11111111-1111-1111-1111-111111111111"
	resp := performModel(h, http.MethodPost, "/api/v1/svc/models/import", `{"source":" huggingface ","repo_id":" Qwen/Qwen3-0.6B ","revision":" main ","idempotency_key":" import-hf ","webhook_url":" https://example.invalid/hook "}`, tenant)
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if got := string(resp.Header.Get("Location")); got != "/api/v1/tasks/task-hf" {
		t.Fatalf("Location = %q", got)
	}
	if client.lastTenantID != tenant || client.lastImport == nil {
		t.Fatalf("forwarded tenant/request = %q/%+v", client.lastTenantID, client.lastImport)
	}
	if got := client.lastImport.GetSource(); got != "huggingface" {
		t.Fatalf("source = %q", got)
	}
	if got := client.lastImport.GetRepoId(); got != "Qwen/Qwen3-0.6B" {
		t.Fatalf("repo_id = %q", got)
	}
	if got := client.lastImport.GetRevision(); got != "main" {
		t.Fatalf("revision = %q", got)
	}
	if got := client.lastImport.GetIdempotencyKey(); got != "import-hf" {
		t.Fatalf("idempotency_key = %q", got)
	}
	if got := client.lastImport.GetWebhookUrl(); got != "https://example.invalid/hook" {
		t.Fatalf("webhook_url = %q", got)
	}
}

func TestImportModelModelScopeDefaultsRevision(t *testing.T) {
	client := &fakeModelClient{importResp: &commonv1.AsyncTaskRef{TaskId: "task-ms"}}
	h := setupModelTestServer(t, client)
	resp := performModel(h, http.MethodPost, "/api/v1/svc/models/import", `{"source":"modelscope","repo_id":"Qwen/Qwen3-0.6B","idempotency_key":"import-ms"}`, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if got := string(resp.Header.Get("Location")); got != "/api/v1/tasks/task-ms" {
		t.Fatalf("Location = %q", got)
	}
	if client.lastImport == nil || client.lastImport.GetRevision() != "main" {
		t.Fatalf("request = %+v", client.lastImport)
	}
}

func TestImportModelRejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "source", body: `{"repo_id":"Qwen/Qwen3-0.6B","idempotency_key":"k"}`},
		{name: "repo_id", body: `{"source":"huggingface","idempotency_key":"k"}`},
		{name: "idempotency_key", body: `{"source":"huggingface","repo_id":"Qwen/Qwen3-0.6B"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeModelClient{importResp: &commonv1.AsyncTaskRef{TaskId: "unexpected"}}
			h := setupModelTestServer(t, client)
			resp := performModel(h, http.MethodPost, "/api/v1/svc/models/import", tt.body, "11111111-1111-1111-1111-111111111111")
			if resp.StatusCode() != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
			}
			if client.lastImport != nil {
				t.Fatalf("invalid request was forwarded: %+v", client.lastImport)
			}
		})
	}
}

func TestImportModelRejectsUnsupportedSource(t *testing.T) {
	client := &fakeModelClient{importResp: &commonv1.AsyncTaskRef{TaskId: "unexpected"}}
	h := setupModelTestServer(t, client)
	resp := performModel(h, http.MethodPost, "/api/v1/svc/models/import", `{"source":"github","repo_id":"Qwen/Qwen3-0.6B","idempotency_key":"k"}`, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastImport != nil {
		t.Fatalf("unsupported source was forwarded: %+v", client.lastImport)
	}
}

func TestImportModelRejectsCredentialField(t *testing.T) {
	client := &fakeModelClient{importResp: &commonv1.AsyncTaskRef{TaskId: "unexpected"}}
	h := setupModelTestServer(t, client)
	resp := performModel(h, http.MethodPost, "/api/v1/svc/models/import", `{"source":"huggingface","repo_id":"Qwen/Qwen3-0.6B","idempotency_key":"k","token":"secret"}`, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
	if client.lastImport != nil {
		t.Fatalf("credential-bearing request was forwarded: %+v", client.lastImport)
	}
}

func TestImportModelMapsGRPCError(t *testing.T) {
	client := &fakeModelClient{err: status.Error(codes.Unavailable, "downstream unavailable")}
	h := setupModelTestServer(t, client)
	resp := performModel(h, http.MethodPost, "/api/v1/svc/models/import", `{"source":"huggingface","repo_id":"Qwen/Qwen3-0.6B","idempotency_key":"k"}`, "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
}

func TestGetModelMapsNotFound(t *testing.T) {
	h := setupModelTestServer(t, &fakeModelClient{err: status.Error(codes.NotFound, "not found")})
	resp := performModel(h, http.MethodGet, "/api/v1/svc/models/22222222-2222-2222-2222-222222222222", "", "11111111-1111-1111-1111-111111111111")
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", resp.StatusCode(), resp.Body())
	}
}
