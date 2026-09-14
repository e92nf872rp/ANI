package router

// GET /api/v1/overview 聚合统计的单元测试：计数正确性、翻页走尽、
// deleted 排除、部分成功语义（任一数据源失败该部分为空、整体仍 200）、
// 客户端未装配时该部分为空。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	commonv1 "github.com/kubercloud/ani/pkg/generated/pb/common/v1"
	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	kbv1 "github.com/kubercloud/ani/pkg/generated/pb/kb/v1"
	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// overviewModelFake 在 fakeModelClient 基础上覆写 ListModels，支持多页游标。
type overviewModelFake struct {
	fakeModelClient
	pages [][]*modelv1.Model
	calls int
	err   error
}

func (f *overviewModelFake) ListModels(_ context.Context, tenantID, _, _, _, _ string, _ int32, _ string) (*modelv1.ListModelsResponse, error) {
	f.calls++
	f.lastTenantID = tenantID
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return &modelv1.ListModelsResponse{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	meta := &commonv1.CursorPageMeta{}
	if len(f.pages) > 0 {
		meta.NextCursor = "cursor-" + time.Now().Format("150405.000000000")
	}
	return &modelv1.ListModelsResponse{Models: page, Meta: meta}, nil
}

// overviewInferenceFake 在 fakeInferenceClient 基础上覆写 ListInferenceServices。
type overviewInferenceFake struct {
	fakeInferenceClient
	items []*inferencecontrolv1.InferenceService
	err   error
}

func (f *overviewInferenceFake) ListInferenceServices(_ context.Context, tenantID string) (*inferencecontrolv1.ListInferenceServicesResponse, error) {
	f.lastTenantID = tenantID
	if f.err != nil {
		return nil, f.err
	}
	return &inferencecontrolv1.ListInferenceServicesResponse{Items: f.items}, nil
}

// overviewKBFake 在 fakeKBClient 基础上覆写 ListKBs，支持多页游标。
type overviewKBFake struct {
	fakeKBClient
	pages [][]*kbv1.KnowledgeBase
	calls int
	err   error
}

func (f *overviewKBFake) ListKBs(_ context.Context, tenantID string, _ int32, _ string) (*kbv1.ListKBsResponse, error) {
	f.calls++
	f.lastTenantID = tenantID
	if f.err != nil {
		return nil, f.err
	}
	if len(f.pages) == 0 {
		return &kbv1.ListKBsResponse{}, nil
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	meta := &commonv1.CursorPageMeta{}
	if len(f.pages) > 0 {
		meta.NextCursor = "kb-cursor"
	}
	return &kbv1.ListKBsResponse{Kbs: page, Meta: meta}, nil
}

func setupOverviewTestServer(t *testing.T, api *instanceAPI, model ModelServiceClient, inference InferenceControlClient, kb KBGRPCClient) *server.Hertz {
	t.Helper()
	prevModel := modelServiceClient
	prevInference := inferenceControlClient
	prevKB := kbInjectedClient
	modelServiceClient = model
	inferenceControlClient = inference
	kbInjectedClient = kb
	t.Cleanup(func() {
		modelServiceClient = prevModel
		inferenceControlClient = prevInference
		kbInjectedClient = prevKB
	})
	h := server.Default()
	h.Use(middleware.RequestID())
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		if tenantID := string(c.GetHeader("X-Dev-Tenant-ID")); tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
		c.Next(ctx)
	})
	registerInstancesWithRuntime(
		h.Group("/api/v1"),
		nil,
		nil,
		false,
		nil,
		nil,
		nil,
		&InstanceRuntime{
			Service:    api.service,
			Store:      api.store,
			Operations: api.operations,
		},
		nil,
	)
	return h
}

func performOverview(h *server.Hertz, tenant string) *protocol.Response {
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
	if tenant != "" {
		headers = append(headers, ut.Header{Key: "X-Dev-Tenant-ID", Value: tenant})
	}
	return ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/overview", nil, headers...).Result()
}

func mustUpsertInstanceRecord(t *testing.T, api *instanceAPI, tenantID, instanceID, name string, state ports.WorkloadState) {
	t.Helper()
	record := ports.WorkloadInstanceRecord{
		TenantID:   tenantID,
		InstanceID: instanceID,
		Name:       name,
		Kind:       ports.WorkloadKindVM,
		Status:     ports.WorkloadStatus{State: state},
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := api.store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatalf("UpsertStatus(%s) error = %v", instanceID, err)
	}
}

func TestConsoleOverviewAggregatesCounts(t *testing.T) {
	api := newInstanceAPI()
	// 实例：running + failed 计入；deleted 排除。
	mustUpsertInstanceRecord(t, api, "tenant-a", "inst-1", "vm-running", ports.WorkloadStateRunning)
	mustUpsertInstanceRecord(t, api, "tenant-a", "inst-2", "vm-failed", ports.WorkloadStateFailed)
	mustUpsertInstanceRecord(t, api, "tenant-a", "inst-3", "vm-deleted", ports.WorkloadStateDeleted)
	// 其他租户不计。
	mustUpsertInstanceRecord(t, api, "tenant-b", "inst-4", "vm-other-tenant", ports.WorkloadStateRunning)

	models := &overviewModelFake{pages: [][]*modelv1.Model{
		{
			{Id: "m1", Status: "ready"},
			{Id: "m2", Status: "downloading"},
		},
		{
			{Id: "m3", Status: "ready"},
			{Id: "m4", Status: "error"},
			{Id: "m5", Status: "deleted"},
		},
	}}
	inferences := &overviewInferenceFake{items: []*inferencecontrolv1.InferenceService{
		{Id: "s1", Status: "running"},
		{Id: "s2", Status: "running"},
		{Id: "s3", Status: "failed"},
	}}
	kbs := &overviewKBFake{pages: [][]*kbv1.KnowledgeBase{
		{
			{Id: "kb1", Status: "active"},
			{Id: "kb2", Status: "rebuilding"},
		},
		{
			{Id: "kb3", Status: "deleted"},
		},
	}}

	h := setupOverviewTestServer(t, api, models, inferences, kbs)
	resp := performOverview(h, "tenant-a")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode(), resp.Body())
	}

	var got consoleOverviewResponse
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// 实例：2 条（deleted 与跨租户排除），分布含全部 8 个契约键。
	if got.Instances.Total != 2 {
		t.Fatalf("instances.total = %d, want 2", got.Instances.Total)
	}
	if got.Instances.ByState["running"] != 1 || got.Instances.ByState["failed"] != 1 {
		t.Fatalf("instances.by_state = %v, want running=1 failed=1", got.Instances.ByState)
	}
	if _, exists := got.Instances.ByState["deleted"]; exists {
		t.Fatalf("instances.by_state should not contain deleted key: %v", got.Instances.ByState)
	}
	for _, state := range []string{"pending", "provisioning", "starting", "stopping", "stopped", "deleting"} {
		if count, ok := got.Instances.ByState[state]; !ok || count != 0 {
			t.Fatalf("instances.by_state[%s] = (%d, %v), want zero-value key present", state, count, ok)
		}
	}

	// 模型：翻页走尽后 4 条（m5 deleted 排除），分布正确。
	if got.Models.Total != 4 {
		t.Fatalf("models.total = %d, want 4", got.Models.Total)
	}
	if got.Models.ByStatus["ready"] != 2 || got.Models.ByStatus["downloading"] != 1 || got.Models.ByStatus["error"] != 1 {
		t.Fatalf("models.by_status = %v, want ready=2 downloading=1 error=1", got.Models.ByStatus)
	}
	if models.calls != 2 {
		t.Fatalf("model list calls = %d, want 2 (pagination to exhaustion)", models.calls)
	}

	// 推理服务：全量单次调用，3 条。
	if got.InferenceServices.Total != 3 {
		t.Fatalf("inference_services.total = %d, want 3", got.InferenceServices.Total)
	}
	if got.InferenceServices.ByStatus["running"] != 2 || got.InferenceServices.ByStatus["failed"] != 1 {
		t.Fatalf("inference_services.by_status = %v, want running=2 failed=1", got.InferenceServices.ByStatus)
	}

	// 知识库：翻页走尽后 2 条（deleted 排除）。
	if got.KnowledgeBases.Total != 2 {
		t.Fatalf("knowledge_bases.total = %d, want 2", got.KnowledgeBases.Total)
	}
	if got.KnowledgeBases.ByStatus["active"] != 1 || got.KnowledgeBases.ByStatus["rebuilding"] != 1 {
		t.Fatalf("knowledge_bases.by_status = %v, want active=1 rebuilding=1", got.KnowledgeBases.ByStatus)
	}
	if kbs.calls != 2 {
		t.Fatalf("kb list calls = %d, want 2 (pagination to exhaustion)", kbs.calls)
	}
}

func TestConsoleOverviewPartialSuccessOnSourceFailure(t *testing.T) {
	api := newInstanceAPI()
	// models 数据源失败：models 部分为空，其余数据源正常计数，整体仍 200。
	models := &overviewModelFake{err: errors.New("model-service unavailable")}
	inferences := &overviewInferenceFake{items: []*inferencecontrolv1.InferenceService{{Id: "s1", Status: "running"}}}
	kbs := &overviewKBFake{}

	h := setupOverviewTestServer(t, api, models, inferences, kbs)
	resp := performOverview(h, "tenant-a")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode(), resp.Body())
	}
	var got consoleOverviewResponse
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Models.Total != 0 || len(got.Models.ByStatus) != 4 || got.Models.ByStatus["ready"] != 0 {
		t.Fatalf("models = %+v, want empty section (total=0, fixed zero keys)", got.Models)
	}
	if got.InferenceServices.Total != 1 || got.InferenceServices.ByStatus["running"] != 1 {
		t.Fatalf("inference_services = %+v, want total=1 running=1", got.InferenceServices)
	}
	if got.KnowledgeBases.Total != 0 {
		t.Fatalf("knowledge_bases.total = %d, want 0", got.KnowledgeBases.Total)
	}
}

func TestConsoleOverviewEmptySectionWhenClientsNotConfigured(t *testing.T) {
	api := newInstanceAPI()
	h := setupOverviewTestServer(t, api, nil, nil, nil)
	resp := performOverview(h, "tenant-a")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode(), resp.Body())
	}
	var got consoleOverviewResponse
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Models.Total != 0 || got.InferenceServices.Total != 0 || got.KnowledgeBases.Total != 0 {
		t.Fatalf("services sections = %+v, want zero totals when clients not configured", got)
	}
}

func TestConsoleOverviewEmptyDataReturnsZeroCounts(t *testing.T) {
	api := newInstanceAPI()
	h := setupOverviewTestServer(t, api,
		&overviewModelFake{},
		&overviewInferenceFake{},
		&overviewKBFake{},
	)
	resp := performOverview(h, "tenant-a")
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode(), resp.Body())
	}
	var got consoleOverviewResponse
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Instances.Total != 0 || got.Models.Total != 0 || got.InferenceServices.Total != 0 || got.KnowledgeBases.Total != 0 {
		t.Fatalf("overview = %+v, want all totals 0", got)
	}
	if len(got.Instances.ByState) != 8 {
		t.Fatalf("instances.by_state keys = %d (%v), want 8 fixed enum keys", len(got.Instances.ByState), got.Instances.ByState)
	}
}

// 编译期确认测试替身满足完整接口（覆写方法 + 内嵌 fake 的其余方法）。
var (
	_ ModelServiceClient     = (*overviewModelFake)(nil)
	_ InferenceControlClient = (*overviewInferenceFake)(nil)
	_ KBGRPCClient           = (*overviewKBFake)(nil)
)
