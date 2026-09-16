package router

// GET /api/v1/overview —— Console 首页概览统计聚合端点。
// 契约：api/openapi/v1.yaml /overview（operationId getConsoleOverview）。
// 数据面：实例复用本进程实例链路（刷新 + 全量列表 + 孤儿合并，与 /instances
// 列表同口径）；model/inference/kb 三类经既有 gRPC 客户端计数，后端服务零改动。
// 口径：四类均不含 deleted；部分成功语义——任一数据源失败不整体报错，
// 该部分以空计数（total=0、分布全 0）返回，整体仍 200，失败记 WARN 日志。

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
)

const (
	// overviewListPageLimit 是概览计数翻页的每页大小（models/kb 契约上限以内，
	// 后端自行钳制）。
	overviewListPageLimit = 100
	// overviewMaxPages 是单个数据源的翻页硬上限，防止 cursor 异常导致死循环；
	// 触顶时以已统计部分返回。
	overviewMaxPages = 50

	// overviewStatusDeleted 是各资源契约中"已删除"状态值，概览统计不计。
	overviewStatusDeleted = "deleted"
)

// consoleStatusCounts 是按状态计数的分布；预置契约枚举键（未出现为 0，
// 不省略字段），未知状态键也会计入以如实反映后端数据。
type consoleStatusCounts map[string]int64

func newConsoleStatusCounts(keys ...string) consoleStatusCounts {
	counts := make(consoleStatusCounts, len(keys)+1)
	for _, key := range keys {
		counts[key] = 0
	}
	return counts
}

func (counts consoleStatusCounts) bump(status string) {
	if status == "" {
		return
	}
	counts[status]++
}

type consoleOverviewResponse struct {
	Instances         consoleInstanceOverview `json:"instances"`
	InferenceServices consoleStatusOverview   `json:"inference_services"`
	Models            consoleStatusOverview   `json:"models"`
	KnowledgeBases    consoleStatusOverview   `json:"knowledge_bases"`
}

// consoleInstanceOverview 对应契约 ConsoleInstanceOverview（实例用 by_state）。
type consoleInstanceOverview struct {
	Total   int64               `json:"total"`
	ByState consoleStatusCounts `json:"by_state"`
}

// consoleStatusOverview 对应契约中推理服务/模型/知识库三块（by_status）。
type consoleStatusOverview struct {
	Total    int64               `json:"total"`
	ByStatus consoleStatusCounts `json:"by_status"`
}

func newConsoleInstanceOverview() consoleInstanceOverview {
	return consoleInstanceOverview{
		ByState: newConsoleStatusCounts(
			"pending", "provisioning", "starting", "running",
			"stopping", "stopped", "failed", "deleting",
		),
	}
}

func newConsoleStatusOverview(keys ...string) consoleStatusOverview {
	return consoleStatusOverview{ByStatus: newConsoleStatusCounts(keys...)}
}

// registerConsoleOverview 注册 Console 首页概览统计路由；由
// registerInstancesWithRuntime 在实例路由注册后调用（复用其 api 实例）。
func registerConsoleOverview(v1 *route.RouterGroup, api *instanceAPI) {
	v1.GET("/overview", api.consoleOverview)
}

// consoleOverview 实现 GET /api/v1/overview：四类数据源并行计数后组装响应。
// 部分成功语义：任一数据源失败仅记 WARN 日志，该部分以空计数返回，整体仍 200。
func (api *instanceAPI) consoleOverview(ctx context.Context, c *app.RequestContext) {
	tenantID := instanceTenantID(c)

	var wg sync.WaitGroup
	instances := newConsoleInstanceOverview()
	inferenceServices := newConsoleStatusOverview("pending", "deploying", "running", "stopping", "stopped", "failed")
	models := newConsoleStatusOverview("pending", "downloading", "ready", "error")
	knowledgeBases := newConsoleStatusOverview("active", "rebuilding")

	run := func(source string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				slog.Warn("console overview data source degraded, returning empty section",
					"source", source, "tenant_id", tenantID, "err", err)
			}
		}()
	}
	run("instances", func() error { return api.countOverviewInstances(ctx, tenantID, &instances) })
	run("inference_services", func() error { return countOverviewInferenceServices(ctx, tenantID, &inferenceServices) })
	run("models", func() error { return countOverviewModels(ctx, tenantID, &models) })
	run("knowledge_bases", func() error { return countOverviewKnowledgeBases(ctx, tenantID, &knowledgeBases) })
	wg.Wait()

	c.JSON(http.StatusOK, consoleOverviewResponse{
		Instances:         instances,
		InferenceServices: inferenceServices,
		Models:            models,
		KnowledgeBases:    knowledgeBases,
	})
}

// countOverviewInstances 按 GET /instances 列表同口径统计实例：
// 先按需刷新 store 状态，再取全量并合并孤儿 Deployment，deleted 不计。
func (api *instanceAPI) countOverviewInstances(ctx context.Context, tenantID string, out *consoleInstanceOverview) error {
	api.refreshStoreStatuses(ctx, tenantID, "")
	records, err := api.service.List(ctx, ports.WorkloadInstanceListRequest{TenantID: tenantID})
	if err != nil {
		return fmt.Errorf("instance list failed: %w", err)
	}
	existing := make(map[string]struct{}, len(records)*2)
	for _, record := range records {
		existing[record.InstanceID] = struct{}{}
		existing[record.Name] = struct{}{}
	}
	for _, orphan := range api.discoverOrphanDeployments(ctx, tenantID) {
		if _, found := existing[orphan.InstanceID]; found {
			continue
		}
		records = append(records, orphan)
		existing[orphan.InstanceID] = struct{}{}
	}
	for _, record := range records {
		if record.Status.State == ports.WorkloadStateDeleted {
			continue
		}
		out.Total++
		out.ByState.bump(string(record.Status.State))
	}
	return nil
}

// countOverviewInferenceServices 统计推理服务（控制面 list 为全量返回，单次调用）。
func countOverviewInferenceServices(ctx context.Context, tenantID string, out *consoleStatusOverview) error {
	if inferenceControlClient == nil {
		return errors.New("inference-service gRPC client not configured")
	}
	resp, err := inferenceControlClient.ListInferenceServices(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("inference service list failed: %w", err)
	}
	for _, service := range resp.GetItems() {
		status := service.GetStatus()
		if status == overviewStatusDeleted {
			continue
		}
		out.Total++
		out.ByStatus.bump(status)
	}
	return nil
}

// countOverviewModels 翻页统计模型（不含 status=deleted）。
func countOverviewModels(ctx context.Context, tenantID string, out *consoleStatusOverview) error {
	if modelServiceClient == nil {
		return errors.New("model-service gRPC client not configured")
	}
	cursor := ""
	for page := 0; page < overviewMaxPages; page++ {
		resp, err := modelServiceClient.ListModels(ctx, tenantID, "", "", "", "", overviewListPageLimit, cursor)
		if err != nil {
			return fmt.Errorf("model list failed: %w", err)
		}
		for _, model := range resp.GetModels() {
			status := model.GetStatus()
			if status == overviewStatusDeleted {
				continue
			}
			out.Total++
			out.ByStatus.bump(status)
		}
		cursor = resp.GetMeta().GetNextCursor()
		if cursor == "" {
			return nil
		}
	}
	return nil
}

// countOverviewKnowledgeBases 翻页统计知识库（不含 status=deleted）。
func countOverviewKnowledgeBases(ctx context.Context, tenantID string, out *consoleStatusOverview) error {
	if kbInjectedClient == nil {
		return errors.New("kb-service gRPC client not configured")
	}
	cursor := ""
	for page := 0; page < overviewMaxPages; page++ {
		resp, err := kbInjectedClient.ListKBs(ctx, tenantID, overviewListPageLimit, cursor)
		if err != nil {
			return fmt.Errorf("knowledge base list failed: %w", err)
		}
		for _, kb := range resp.GetKbs() {
			status := kb.GetStatus()
			if status == overviewStatusDeleted {
				continue
			}
			out.Total++
			out.ByStatus.bump(status)
		}
		cursor = resp.GetMeta().GetNextCursor()
		if cursor == "" {
			return nil
		}
	}
	return nil
}
