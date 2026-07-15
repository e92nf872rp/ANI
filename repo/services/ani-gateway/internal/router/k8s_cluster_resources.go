package router

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

type k8sClusterAPI struct{ service ports.K8sClusterService }
type k8sClusterCreateRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Name           string `json:"name"`
	Version        string `json:"version"`
}
type k8sClusterUpgradeRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	Version        string `json:"version"`
}
type k8sClusterNodePoolGPURequest struct {
	Vendor       string `json:"vendor"`
	Model        string `json:"model"`
	Count        int    `json:"count"`
	ResourceName string `json:"resource_name"`
}
type k8sClusterNodePoolCreateRequest struct {
	IdempotencyKey string                       `json:"idempotency_key"`
	Name           string                       `json:"name"`
	NodeCount      int                          `json:"node_count"`
	InstanceType   string                       `json:"instance_type"`
	GPU            k8sClusterNodePoolGPURequest `json:"gpu"`
}
type k8sClusterNodePoolUpdateRequest struct {
	IdempotencyKey string                       `json:"idempotency_key"`
	NodeCount      int                          `json:"node_count"`
	InstanceType   string                       `json:"instance_type"`
	GPU            k8sClusterNodePoolGPURequest `json:"gpu"`
}
type k8sClusterProxyRequest struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Method         string            `json:"method"`
	Path           string            `json:"path"`
	Query          map[string]string `json:"query"`
	Body           map[string]any    `json:"body"`
}
type k8sClusterResponse struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	Name       string                 `json:"name"`
	Version    string                 `json:"version,omitempty"`
	State      string                 `json:"state"`
	Reason     string                 `json:"reason,omitempty"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
	CreatedAt  string                 `json:"created_at"`
	UpdatedAt  string                 `json:"updated_at"`
}
type k8sClusterNodePoolGPUResponse struct {
	Vendor       string `json:"vendor,omitempty"`
	Model        string `json:"model,omitempty"`
	Count        int    `json:"count,omitempty"`
	ResourceName string `json:"resource_name,omitempty"`
}
type k8sClusterNodePoolResponse struct {
	ID           string                        `json:"id"`
	TenantID     string                        `json:"tenant_id"`
	ClusterID    string                        `json:"cluster_id"`
	Name         string                        `json:"name"`
	NodeCount    int                           `json:"node_count"`
	InstanceType string                        `json:"instance_type"`
	GPU          k8sClusterNodePoolGPUResponse `json:"gpu"`
	State        string                        `json:"state"`
	Reason       string                        `json:"reason,omitempty"`
	DevProfile   coreDevProfileResponse        `json:"dev_profile"`
	CreatedAt    string                        `json:"created_at"`
	UpdatedAt    string                        `json:"updated_at"`
}
type k8sClusterKubeconfigResponse struct {
	ClusterID  string                 `json:"cluster_id"`
	TenantID   string                 `json:"tenant_id"`
	Server     string                 `json:"server"`
	Namespace  string                 `json:"namespace"`
	CAData     string                 `json:"ca_data"`
	Token      string                 `json:"token"`
	Kubeconfig string                 `json:"kubeconfig"`
	ExpiresAt  string                 `json:"expires_at"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}
type k8sClusterProxyResponse struct {
	ClusterID  string                 `json:"cluster_id"`
	TenantID   string                 `json:"tenant_id"`
	Method     string                 `json:"method"`
	Path       string                 `json:"path"`
	Query      map[string]string      `json:"query"`
	StatusCode int                    `json:"status_code"`
	Headers    map[string]string      `json:"headers"`
	Body       map[string]any         `json:"body"`
	ProxiedAt  string                 `json:"proxied_at"`
	DevProfile coreDevProfileResponse `json:"dev_profile"`
}
type k8sClusterWorkloadResponse struct {
	Name          string                 `json:"name"`
	Namespace     string                 `json:"namespace"`
	Kind          string                 `json:"kind"`
	Replicas      int                    `json:"replicas"`
	ReadyReplicas int                    `json:"ready_replicas"`
	Image         string                 `json:"image,omitempty"`
	Status        string                 `json:"status"`
	CreatedAt     string                 `json:"created_at"`
	DevProfile    coreDevProfileResponse `json:"dev_profile"`
}

func newK8sClusterAPI() *k8sClusterAPI {
	return newK8sClusterAPIWithService(nil)
}

func newK8sClusterAPIWithService(service ports.K8sClusterService) *k8sClusterAPI {
	if service == nil {
		service = runtimeadapter.NewLocalK8sClusterService()
	}
	return &k8sClusterAPI{service: service}
}
func registerK8sClusterResourcesWithService(v1 *route.RouterGroup, service ports.K8sClusterService) {
	api := newK8sClusterAPIWithService(service)
	v1.GET("/k8s-clusters", api.listClusters)
	v1.POST("/k8s-clusters", api.createCluster)
	v1.GET("/k8s-clusters/:cluster_id", api.getCluster)
	v1.DELETE("/k8s-clusters/:cluster_id", api.deleteCluster)
	v1.POST("/k8s-clusters/:cluster_id/upgrade", api.upgradeCluster)
	v1.GET("/k8s-clusters/:cluster_id/node-pools", api.listNodePools)
	v1.POST("/k8s-clusters/:cluster_id/node-pools", api.createNodePool)
	v1.GET("/k8s-clusters/:cluster_id/node-pools/:node_pool_id", api.getNodePool)
	v1.PATCH("/k8s-clusters/:cluster_id/node-pools/:node_pool_id", api.updateNodePool)
	v1.DELETE("/k8s-clusters/:cluster_id/node-pools/:node_pool_id", api.deleteNodePool)
	v1.GET("/k8s-clusters/:cluster_id/kubeconfig", api.getKubeconfig)
	v1.POST("/k8s-clusters/:cluster_id/proxy", api.proxy)
	v1.GET("/k8s-clusters/:cluster_id/workloads", api.listWorkloads)
}
func (api *k8sClusterAPI) createCluster(ctx context.Context, c *app.RequestContext) {
	var req k8sClusterCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid k8s cluster request")
		return
	}
	if req.Version == "requires-real-provider" {
		writeDemoError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", "k8s cluster create precondition failed: real provider is not configured for this local profile")
		return
	}
	rec, err := api.service.CreateCluster(ctx, ports.K8sClusterCreateRequest{TenantID: demoTenantID(c), IdempotencyKey: req.IdempotencyKey, Name: req.Name, Version: req.Version})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusCreated, k8sClusterFromRecord(rec))
}
func (api *k8sClusterAPI) listClusters(ctx context.Context, c *app.RequestContext) {
	recs, err := api.service.ListClusters(ctx, ports.K8sClusterListRequest{TenantID: demoTenantID(c)})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	items := make([]k8sClusterResponse, 0, len(recs))
	for _, r := range recs {
		items = append(items, k8sClusterFromRecord(r))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}
func (api *k8sClusterAPI) getCluster(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.GetCluster(ctx, ports.K8sClusterGetRequest{TenantID: demoTenantID(c), ClusterID: c.Param("cluster_id")})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, k8sClusterFromRecord(rec))
}
func (api *k8sClusterAPI) deleteCluster(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.DeleteCluster(ctx, ports.K8sClusterGetRequest{TenantID: demoTenantID(c), ClusterID: c.Param("cluster_id")})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, k8sClusterFromRecord(rec))
}
func (api *k8sClusterAPI) upgradeCluster(ctx context.Context, c *app.RequestContext) {
	var req k8sClusterUpgradeRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid k8s cluster upgrade request")
		return
	}
	rec, err := api.service.UpgradeCluster(ctx, ports.K8sClusterUpgradeRequest{TenantID: demoTenantID(c), ClusterID: c.Param("cluster_id"), IdempotencyKey: req.IdempotencyKey, Version: req.Version})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, k8sClusterFromRecord(rec))
}
func (api *k8sClusterAPI) createNodePool(ctx context.Context, c *app.RequestContext) {
	var req k8sClusterNodePoolCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid k8s cluster node pool request")
		return
	}
	rec, err := api.service.CreateNodePool(ctx, ports.K8sClusterNodePoolCreateRequest{
		TenantID:       demoTenantID(c),
		ClusterID:      c.Param("cluster_id"),
		IdempotencyKey: req.IdempotencyKey,
		Name:           req.Name,
		NodeCount:      req.NodeCount,
		InstanceType:   req.InstanceType,
		GPU:            k8sClusterNodePoolGPUFromRequest(req.GPU),
	})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusCreated, k8sClusterNodePoolFromRecord(rec))
}
func (api *k8sClusterAPI) listNodePools(ctx context.Context, c *app.RequestContext) {
	recs, err := api.service.ListNodePools(ctx, ports.K8sClusterNodePoolListRequest{TenantID: demoTenantID(c), ClusterID: c.Param("cluster_id")})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	items := make([]k8sClusterNodePoolResponse, 0, len(recs))
	for _, rec := range recs {
		items = append(items, k8sClusterNodePoolFromRecord(rec))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}
func (api *k8sClusterAPI) getNodePool(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.GetNodePool(ctx, ports.K8sClusterNodePoolGetRequest{TenantID: demoTenantID(c), ClusterID: c.Param("cluster_id"), NodePoolID: c.Param("node_pool_id")})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, k8sClusterNodePoolFromRecord(rec))
}
func (api *k8sClusterAPI) updateNodePool(ctx context.Context, c *app.RequestContext) {
	var req k8sClusterNodePoolUpdateRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid k8s cluster node pool update request")
		return
	}
	rec, err := api.service.UpdateNodePool(ctx, ports.K8sClusterNodePoolUpdateRequest{
		TenantID:       demoTenantID(c),
		ClusterID:      c.Param("cluster_id"),
		NodePoolID:     c.Param("node_pool_id"),
		IdempotencyKey: req.IdempotencyKey,
		NodeCount:      req.NodeCount,
		InstanceType:   req.InstanceType,
		GPU:            k8sClusterNodePoolGPUFromRequest(req.GPU),
	})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, k8sClusterNodePoolFromRecord(rec))
}
func (api *k8sClusterAPI) deleteNodePool(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.DeleteNodePool(ctx, ports.K8sClusterNodePoolGetRequest{TenantID: demoTenantID(c), ClusterID: c.Param("cluster_id"), NodePoolID: c.Param("node_pool_id")})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, k8sClusterNodePoolFromRecord(rec))
}
func (api *k8sClusterAPI) getKubeconfig(ctx context.Context, c *app.RequestContext) {
	rec, err := api.service.GetKubeconfig(ctx, ports.K8sClusterKubeconfigRequest{TenantID: demoTenantID(c), ClusterID: c.Param("cluster_id")})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, k8sClusterKubeconfigFromRecord(rec))
}
func (api *k8sClusterAPI) proxy(ctx context.Context, c *app.RequestContext) {
	var req k8sClusterProxyRequest
	if err := c.BindJSON(&req); err != nil {
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid k8s cluster proxy request")
		return
	}
	rec, err := api.service.Proxy(ctx, ports.K8sClusterProxyRequest{
		TenantID:       demoTenantID(c),
		ClusterID:      c.Param("cluster_id"),
		IdempotencyKey: req.IdempotencyKey,
		Method:         req.Method,
		Path:           req.Path,
		Query:          req.Query,
		Body:           req.Body,
	})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, k8sClusterProxyFromRecord(rec))
}
func (api *k8sClusterAPI) listWorkloads(ctx context.Context, c *app.RequestContext) {
	recs, err := api.service.ListWorkloads(ctx, ports.K8sClusterWorkloadListRequest{
		TenantID:  demoTenantID(c),
		ClusterID: c.Param("cluster_id"),
		Namespace: c.Query("namespace"),
		Kind:      c.Query("kind"),
	})
	if err != nil {
		writeK8sClusterError(c, err)
		return
	}
	items := make([]k8sClusterWorkloadResponse, 0, len(recs))
	for _, rec := range recs {
		items = append(items, k8sClusterWorkloadFromRecord(rec))
	}
	c.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items), "next_cursor": nil})
}
func k8sClusterFromRecord(r ports.K8sClusterRecord) k8sClusterResponse {
	devProfile := localCoreDevProfile("local-k8s-cluster-service", "Core dev/local profile; vCluster lifecycle is simulated")
	if r.RealProvider && r.Provider == "vcluster" {
		devProfile = coreDevProfileResponse{
			Mode:         "real",
			Provider:     "vcluster-provider",
			RealProvider: true,
			Reason:       "vCluster provider has applied the lifecycle resources; live kubeconfig/proxy validation is gated separately",
		}
	}
	return k8sClusterResponse{ID: r.ClusterID, TenantID: r.TenantID, Name: r.Name, Version: r.Version, State: string(r.State), Reason: r.Reason, DevProfile: devProfile, CreatedAt: time.Unix(r.CreatedAt, 0).UTC().Format(time.RFC3339), UpdatedAt: time.Unix(r.UpdatedAt, 0).UTC().Format(time.RFC3339)}
}
func k8sClusterNodePoolFromRecord(r ports.K8sClusterNodePoolRecord) k8sClusterNodePoolResponse {
	devProfile := localCoreDevProfile("local-k8s-cluster-service", "Core dev/local profile; K8s node pool lifecycle is simulated")
	if r.RealProvider && r.Provider != "" && r.Provider != "local" {
		devProfile = coreDevProfileResponse{
			Mode:         "real",
			Provider:     r.Provider + "-provider",
			RealProvider: true,
			Reason:       "Node pool provider has applied lifecycle intent; live scaling validation is gated separately",
		}
	}
	return k8sClusterNodePoolResponse{
		ID:           r.NodePoolID,
		TenantID:     r.TenantID,
		ClusterID:    r.ClusterID,
		Name:         r.Name,
		NodeCount:    r.NodeCount,
		InstanceType: r.InstanceType,
		GPU: k8sClusterNodePoolGPUResponse{
			Vendor:       r.GPU.Vendor,
			Model:        r.GPU.Model,
			Count:        r.GPU.Count,
			ResourceName: r.GPU.ResourceName,
		},
		State:      string(r.State),
		Reason:     r.Reason,
		DevProfile: devProfile,
		CreatedAt:  time.Unix(r.CreatedAt, 0).UTC().Format(time.RFC3339),
		UpdatedAt:  time.Unix(r.UpdatedAt, 0).UTC().Format(time.RFC3339),
	}
}
func k8sClusterKubeconfigFromRecord(r ports.K8sClusterKubeconfigRecord) k8sClusterKubeconfigResponse {
	return k8sClusterKubeconfigResponse{ClusterID: r.ClusterID, TenantID: r.TenantID, Server: r.Server, Namespace: r.Namespace, CAData: r.CAData, Token: r.Token, Kubeconfig: r.Kubeconfig, ExpiresAt: time.Unix(r.ExpiresAt, 0).UTC().Format(time.RFC3339), DevProfile: localCoreDevProfile("local-k8s-cluster-service", "Core dev/local profile; kubeconfig targets a simulated vCluster endpoint")}
}
func k8sClusterProxyFromRecord(r ports.K8sClusterProxyRecord) k8sClusterProxyResponse {
	return k8sClusterProxyResponse{ClusterID: r.ClusterID, TenantID: r.TenantID, Method: r.Method, Path: r.Path, Query: r.Query, StatusCode: r.StatusCode, Headers: r.Headers, Body: r.Body, ProxiedAt: time.Unix(r.ProxiedAt, 0).UTC().Format(time.RFC3339), DevProfile: localCoreDevProfile("local-k8s-cluster-service", "Core dev/local profile; proxy response is simulated and not forwarded to a real vCluster API server")}
}
func k8sClusterWorkloadFromRecord(r ports.K8sClusterWorkloadRecord) k8sClusterWorkloadResponse {
	return k8sClusterWorkloadResponse{Name: r.Name, Namespace: r.Namespace, Kind: r.Kind, Replicas: r.Replicas, ReadyReplicas: r.ReadyReplicas, Image: r.Image, Status: string(r.Status), CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339), DevProfile: localCoreDevProfile("local-k8s-cluster-service", "Core dev/local profile; workload listing is simulated and not forwarded to a real vCluster API server")}
}
func k8sClusterNodePoolGPUFromRequest(gpu k8sClusterNodePoolGPURequest) ports.K8sClusterNodePoolGPU {
	return ports.K8sClusterNodePoolGPU{Vendor: gpu.Vendor, Model: gpu.Model, Count: gpu.Count, ResourceName: gpu.ResourceName}
}
func writeK8sClusterError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrConflict):
		writeDemoError(c, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, ports.ErrFailedPrecondition):
		writeDemoError(c, http.StatusUnprocessableEntity, "PRECONDITION_FAILED", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}
