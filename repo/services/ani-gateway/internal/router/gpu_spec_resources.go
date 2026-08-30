package router

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/pkg/ports"
)

// gpuSpecAPI implements the 4 GPU spec directory endpoints backed by
// ports.GPUSpecStore (K8s CRD). The existing GET /gpu-specs and
// GET /gpu-specs/:spec_id in gpu_inventory_resources.go use the local
// GPUSpecService; when a GPUSpecStore is injected these routes are
// overridden here to read from the CRD.
type gpuSpecAPI struct {
	store         ports.GPUSpecStore
	inventory     ports.GPUInventory
	instanceStore ports.WorkloadInstanceStore
	// metadataStore enables platform-scoped (RLS-bypass) queries for the
	// cross-tenant GPUSpecInUse check. When nil, specInUse skips the check
	// and logs a warning (no false-negative deletion risk).
	metadataStore ports.MetadataStore
}

// specIDPattern validates spec_id against K8s name conventions.
var specIDPattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// registerGPUSpecResources registers the 4 GPU spec endpoints. When
// store is nil the POST and DELETE handlers return 503; GET endpoints
// fall back to the existing GPUSpecService path.
func registerGPUSpecResources(v1 *route.RouterGroup, store ports.GPUSpecStore, inventory ports.GPUInventory, instanceStore ports.WorkloadInstanceStore, metadataStore ports.MetadataStore) {
	api := &gpuSpecAPI{store: store, inventory: inventory, instanceStore: instanceStore, metadataStore: metadataStore}
	v1.POST("/gpu-specs", api.createGPUSpec)
	v1.DELETE("/gpu-specs/:spec_id", api.deleteGPUSpec)
}

// gpuSpecCreateRequest matches GPUSpecCreateRequest schema (v1.yaml).
type gpuSpecCreateRequest struct {
	SpecID        string `json:"spec_id"`
	GPUType       string `json:"gpu_type"`
	GPUMode       string `json:"gpu_mode"`
	Shares        int    `json:"shares"`
	MBPerShare    int    `json:"mb_per_share"`
	MemoryTotalMB *int64 `json:"memory_total_mb"`
}

// gpuSpecFullResponse matches the GPUSpec schema (POST 201 response).
type gpuSpecFullResponse struct {
	ID               string                       `json:"id"`
	Name             string                       `json:"name"`
	GPUType          string                       `json:"gpu_type"`
	GPUMode          string                       `json:"gpu_mode"`
	MemoryTotalMB    *int64                       `json:"memory_total_mb,omitempty"`
	Shares           int                          `json:"shares"`
	MBPerShare       int                          `json:"mb_per_share"`
	Available        bool                         `json:"available"`
	NodeAffinity     *gpuSpecNodeAffinityResponse `json:"node_affinity,omitempty"`
	VolcanoResources *gpuSpecVolcanoResResponse   `json:"volcano_resources,omitempty"`
}

type gpuSpecNodeAffinityResponse struct {
	GPUSpec          string `json:"gpu_spec,omitempty"`
	GPUSharingSpec   string `json:"gpu_sharing_spec,omitempty"`
	GPUSharingPolicy string `json:"gpu_sharing_policy,omitempty"`
	GPUMode          string `json:"gpu_mode,omitempty"`
}

type gpuSpecVolcanoResResponse struct {
	Wholecard map[string]string `json:"wholecard,omitempty"`
	VGPU      map[string]string `json:"vgpu,omitempty"`
}

func (api *gpuSpecAPI) createGPUSpec(ctx context.Context, c *app.RequestContext) {
	if api.store == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "GPU spec store is not configured")
		return
	}
	idempotencyKey := strings.TrimSpace(string(c.Request.Header.Peek("Idempotency-Key")))
	if idempotencyKey == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "Idempotency-Key header is required")
		return
	}
	var req gpuSpecCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid GPU spec create request")
		return
	}
	specID := strings.TrimSpace(req.SpecID)
	if !specIDPattern.MatchString(specID) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "spec_id must match ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
		return
	}
	if strings.TrimSpace(req.GPUType) == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "gpu_type is required")
		return
	}
	if req.GPUMode != "wholecard" && req.GPUMode != "vgpu" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "gpu_mode must be wholecard or vgpu")
		return
	}
	if req.Shares < 1 {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "shares must be >= 1")
		return
	}
	if req.MBPerShare < 1 {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "mb_per_share must be >= 1")
		return
	}
	// vGPU mode: mb_per_share must be >= volcano vGPU factor (10) to avoid
	// producing volcano.sh/vgpu-memory=0 in the resource translator.
	if req.GPUMode == "vgpu" && req.MBPerShare < 10 {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "mb_per_share must be >= 10 for vgpu mode (volcano vGPU factor)")
		return
	}
	// Validate gpu_type exists in cluster node labels (SPEC §5.2, 422 GPUTypeNotInNodes).
	if api.inventory != nil {
		if !api.gpuTypeExistsInNodes(ctx, req.GPUType, req.GPUMode) {
			writeInstanceError(c, http.StatusUnprocessableEntity, "GPUTypeNotInNodes", "gpu_type does not exist in cluster node labels")
			return
		}
	}
	spec := ports.GPUSpecCRD{
		ID:            specID,
		Name:          specID,
		GPUType:       strings.TrimSpace(req.GPUType),
		GPUMode:       req.GPUMode,
		MemoryTotalMB: derefInt64Ptr(req.MemoryTotalMB),
		Shares:        req.Shares,
		MBPerShare:    req.MBPerShare,
		Available:     true,
		NodeAffinity: ports.GPUSpecNodeAffinity{
			GPUMode: req.GPUMode,
		},
	}
	if req.GPUMode == "wholecard" {
		spec.NodeAffinity.GPUSpec = strings.TrimSpace(req.GPUType)
		// Derive volcano_resources for wholecard mode (SPEC §3.2):
		// nvidia.com/gpu: "{count}" placeholder, translated by VolcanoResourceTranslator.
		spec.VolcanoResources = ports.GPUSpecVolcanoResources{
			Wholecard: map[string]string{"nvidia.com/gpu": "{count}"},
		}
	} else {
		spec.NodeAffinity.GPUSharingSpec = strings.TrimSpace(req.GPUType)
		// Derive gpu_sharing_policy from shares (SPEC §3.2):
		// quarter=4, half=2; aligns to ani.kubercloud.io/gpu-sharing-policy label.
		spec.NodeAffinity.GPUSharingPolicy = sharesToSharingPolicy(req.Shares)
		// Derive volcano_resources for vgpu mode (SPEC §3.2):
		// volcano.sh/vgpu-memory uses {mb_per_share} placeholder (never empty, FR-26),
		// volcano.sh/vgpu-number uses {count} placeholder.
		spec.VolcanoResources = ports.GPUSpecVolcanoResources{
			VGPU: map[string]string{
				"volcano.sh/vgpu-memory": "{mb_per_share}",
				"volcano.sh/vgpu-number": "{count}",
			},
		}
	}
	created, err := api.store.Create(ctx, idempotencyKey, spec)
	if err != nil {
		writeGPUSpecError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gpuSpecFullResponseFromCRD(created))
}

func (api *gpuSpecAPI) deleteGPUSpec(ctx context.Context, c *app.RequestContext) {
	if api.store == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "GPU spec store is not configured")
		return
	}
	idempotencyKey := strings.TrimSpace(string(c.Request.Header.Peek("Idempotency-Key")))
	if idempotencyKey == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "Idempotency-Key header is required")
		return
	}
	specID := c.Param("spec_id")
	// Check if any instances reference this spec (SPEC §5.2, 409 GPUSpecInUse).
	if api.specInUse(ctx, specID) {
		writeInstanceError(c, http.StatusConflict, "GPUSpecInUse", "GPU spec is in use by running instances")
		return
	}
	if err := api.store.Delete(ctx, idempotencyKey, specID); err != nil {
		writeGPUSpecError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// gpuTypeExistsInNodes checks whether gpu_type matches any node label in the
// cluster. For wholecard mode it checks ani.kubercloud.io/gpu-spec; for vgpu
// it checks ani.kubercloud.io/gpu-sharing-spec (SPEC §5.2).
func (api *gpuSpecAPI) gpuTypeExistsInNodes(ctx context.Context, gpuType, gpuMode string) bool {
	nodes, err := api.inventory.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil || len(nodes) == 0 {
		return false
	}
	for _, node := range nodes {
		if gpuMode == "wholecard" && node.GPUSpec == gpuType {
			return true
		}
		if gpuMode == "vgpu" && node.GPUSharingSpec == gpuType {
			return true
		}
	}
	return false
}

// sharesToSharingPolicy maps the shares count to the gpu-sharing-policy
// label value (SPEC §3.2): 4 → "quarter", 2 → "half". Shares values that
// do not map to a known policy return an empty string (the spec is still
// created; the translator will surface a scheduling error if the policy
// is required and missing).
func sharesToSharingPolicy(shares int) string {
	switch shares {
	case 4:
		return "quarter"
	case 2:
		return "half"
	default:
		return ""
	}
}

// specInUse checks whether any non-deleted instance references the given
// spec_id. When metadataStore is available it runs a platform-scoped
// (RLS-bypass) SQL query across all tenants. The query checks both
// gpu_status->>'SpecID' (written by GPUContainer instances) and
// compute_summary->>'SpecID' (written by all instance kinds when a GPUSpec
// is set), so Inference/Notebook/BatchJob references are also detected.
// Otherwise it returns false with a warning: the tenant-scoped
// instanceStore.List cannot perform a cross-tenant in-use check, so the
// fallback is skipped to avoid false-negatives that would allow deleting a
// spec still referenced by another tenant.
func (api *gpuSpecAPI) specInUse(ctx context.Context, specID string) bool {
	if api.metadataStore != nil {
		var count int64
		err := api.metadataStore.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
			return tx.QueryRow(ctx, `
				SELECT COUNT(*) FROM workload_instances
				WHERE state <> 'deleted'
				  AND (gpu_status->>'SpecID' = $1 OR compute_summary->>'SpecID' = $1)
			`, specID).Scan(&count)
		})
		if err != nil {
			slog.Error("gpu spec in-use check failed, fail-closed to prevent deleting a referenced spec",
				"spec_id", specID,
				"err", err,
			)
			return true
		}
		return count > 0
	}
	slog.Warn("gpu spec in-use check skipped: metadataStore not configured, cannot perform cross-tenant check",
		"spec_id", specID,
	)
	return false
}

func gpuSpecFullResponseFromCRD(crd ports.GPUSpecCRD) gpuSpecFullResponse {
	resp := gpuSpecFullResponse{
		ID:           crd.ID,
		Name:         crd.Name,
		GPUType:      crd.GPUType,
		GPUMode:      crd.GPUMode,
		Shares:       crd.Shares,
		MBPerShare:   crd.MBPerShare,
		Available:    crd.Available,
		NodeAffinity: &gpuSpecNodeAffinityResponse{},
	}
	if crd.MemoryTotalMB > 0 {
		mb := crd.MemoryTotalMB
		resp.MemoryTotalMB = &mb
	}
	if crd.NodeAffinity != (ports.GPUSpecNodeAffinity{}) {
		resp.NodeAffinity = &gpuSpecNodeAffinityResponse{
			GPUSpec:          crd.NodeAffinity.GPUSpec,
			GPUSharingSpec:   crd.NodeAffinity.GPUSharingSpec,
			GPUSharingPolicy: crd.NodeAffinity.GPUSharingPolicy,
			GPUMode:          crd.NodeAffinity.GPUMode,
		}
	}
	if len(crd.VolcanoResources.Wholecard) > 0 || len(crd.VolcanoResources.VGPU) > 0 {
		resp.VolcanoResources = &gpuSpecVolcanoResResponse{
			Wholecard: crd.VolcanoResources.Wholecard,
			VGPU:      crd.VolcanoResources.VGPU,
		}
	}
	return resp
}

func writeGPUSpecError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrGPUSpecNotFound):
		writeInstanceError(c, http.StatusNotFound, "GPUSpecNotFound", err.Error())
	case errors.Is(err, ports.ErrGPUSpecConflict):
		writeInstanceError(c, http.StatusConflict, "GPUSpecConflict", err.Error())
	case errors.Is(err, ports.ErrGPUSpecInUse):
		writeInstanceError(c, http.StatusConflict, "GPUSpecInUse", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeInstanceError(c, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}

func derefInt64Ptr(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}
