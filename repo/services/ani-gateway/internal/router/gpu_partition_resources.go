package router

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/pkg/types"
)

// gpuPartitionTaskType / gpuPartitionResourceType are the async task identity
// values persisted on async_tasks rows for cluster GPU split operations
// (GPU-PARTITION-A contract: POST /gpu-inventory/gpu-partitions).
const (
	gpuPartitionTaskType     = "gpu_partition"
	gpuPartitionResourceType = "gpu_inventory"
	// gpuPartitionApplyTimeout bounds the whole apply write sequence (config
	// patch + relabel + plugin restart + registration convergence, §异步执行模型).
	gpuPartitionApplyTimeout = 120 * time.Second
	// gpuPartitionResumeAfter marks a running gpu_partition task as orphaned
	// (apply timeout is 120s; 10x margin) and enables one idempotent
	// lazy-resume re-entry on GET /tasks/{task_id}.
	gpuPartitionResumeAfter = 10 * time.Minute
)

// gpuPartitionAppliers guards against duplicate apply goroutines for the same
// task within one gateway process (POST + concurrent lazy-resume GET).
var gpuPartitionAppliers sync.Map

// gpuPartitionResumeHook is injected by registerGPUPartitionResources and
// consulted by taskAPI.get for running gpu_partition tasks. Package-level
// holder follows the kb/model client injection precedent in this package.
var gpuPartitionResumeHook func(ctx context.Context, tenantID string, task ports.AsyncTaskRecord) ports.AsyncTaskRecord

type gpuPartitionAPI struct {
	planner ports.GPUPartitionPlanner
	tasks   ports.AsyncTaskStore
}

// registerGPUPartitionResources registers POST /gpu-inventory/gpu-partitions
// (BOSS-only cluster GPU split; platform scope enforced by middleware/auth.go).
// When planner is nil the handler returns 503 so the gateway boots in
// local/dev profiles without the kubernetes_rest provider.
func registerGPUPartitionResources(v1 *route.RouterGroup, planner ports.GPUPartitionPlanner, tasks ports.AsyncTaskStore) {
	if tasks == nil {
		tasks = defaultTaskStore
	}
	api := &gpuPartitionAPI{planner: planner, tasks: tasks}
	v1.POST("/gpu-inventory/gpu-partitions", api.create)
	gpuPartitionResumeHook = api.resumeOrphanedTask
}

type gpuPartitionCreateRequest struct {
	Shares int `json:"shares"`
}

func (api *gpuPartitionAPI) create(ctx context.Context, c *app.RequestContext) {
	if api.planner == nil {
		writeInstanceError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", "GPU partition planner is not configured (requires kubernetes_rest provider)")
		return
	}
	idempotencyKey := strings.TrimSpace(string(c.Request.Header.Peek("Idempotency-Key")))
	if idempotencyKey == "" {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "Idempotency-Key header is required")
		return
	}
	var req gpuPartitionCreateRequest
	if err := c.BindJSON(&req); err != nil {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "invalid request body")
		return
	}
	plan, err := api.planner.PlanGPUPartition(ctx, req.Shares)
	if errors.Is(err, ports.ErrInvalid) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if errors.Is(err, ports.ErrNotConfigured) {
		writeInstanceError(c, http.StatusServiceUnavailable, "NOT_CONFIGURED", err.Error())
		return
	}
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "GPU_PARTITION_PLAN_FAILED", err.Error())
		return
	}
	if len(plan.EligibleNodes) == 0 {
		writeInstanceError(c, http.StatusUnprocessableEntity, "NO_IDLE_WHOLECARD_GPUS", "no idle wholecard GPU nodes available for split")
		return
	}
	created, replayed, err := api.tasks.Create(ctx, ports.AsyncTaskRecord{
		TenantID:       instanceTenantID(c),
		IdempotencyKey: idempotencyKey,
		TaskType:       gpuPartitionTaskType,
		ResourceType:   gpuPartitionResourceType,
		Status:         "running",
		AttemptCount:   1,
		MaxAttempts:    1,
		ProgressPct:    0,
		Result:         map[string]any{"shares": plan.Shares},
	})
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "TASK_PERSIST_FAILED", err.Error())
		return
	}
	if !replayed && api.startApply(created) {
		go api.runApply(detachedTaskContext(created), created, plan)
	}
	response := taskResponseFromRecord(created)
	c.Response.Header.Set("Location", "/api/v1/tasks/"+response.ID)
	c.JSON(http.StatusAccepted, gpuPartitionAcceptedFromRecord(created))
}

type gpuPartitionAcceptedResponse struct {
	TaskID      string `json:"task_id"`
	TaskType    string `json:"task_type"`
	Status      string `json:"status"`
	ProgressPct int    `json:"progress_pct"`
}

func gpuPartitionAcceptedFromRecord(record ports.AsyncTaskRecord) gpuPartitionAcceptedResponse {
	return gpuPartitionAcceptedResponse{
		TaskID:      record.ID,
		TaskType:    record.TaskType,
		Status:      record.Status,
		ProgressPct: record.ProgressPct,
	}
}

// detachedTaskContext returns a context that outlives the HTTP request but
// still carries the task owner's tenant identity, so PG-backed task store
// writes (WithTenantTx -> SetDBTenant -> FromContext) succeed inside the
// background apply goroutine. The hertz request ctx must NOT be used here:
// it is pooled and reset once the handler returns. Non-UUID tenant IDs only
// occur with the dev fallback in-memory store, which needs no tenant ctx.
func detachedTaskContext(task ports.AsyncTaskRecord) context.Context {
	if tenantID, err := uuid.Parse(task.TenantID); err == nil {
		return types.WithTenant(context.Background(), &types.TenantContext{TenantID: tenantID})
	}
	return context.Background()
}

// startApply claims the in-process apply slot for the task; false means
// another goroutine already owns it.
func (api *gpuPartitionAPI) startApply(task ports.AsyncTaskRecord) bool {
	key := task.TenantID + "\x00" + task.ID
	if _, loaded := gpuPartitionAppliers.LoadOrStore(key, struct{}{}); loaded {
		return false
	}
	return true
}

// runApply executes the apply write sequence in the background and folds
// stage progress + the final result into the async task record. All store
// errors are logged and swallowed: the apply itself is idempotent, so the
// lazy-resume path can repair a half-written task after a transient failure.
func (api *gpuPartitionAPI) runApply(parent context.Context, task ports.AsyncTaskRecord, plan ports.GPUPartitionPlan) {
	key := task.TenantID + "\x00" + task.ID
	defer gpuPartitionAppliers.Delete(key)
	ctx, cancel := context.WithTimeout(parent, gpuPartitionApplyTimeout)
	defer cancel()

	lastPct := task.ProgressPct
	progress := func(step, total int, message string) {
		if total <= 0 {
			return
		}
		pct := step * 100 / total
		if pct > 95 {
			pct = 95
		}
		lastPct = pct
		if _, err := api.tasks.Update(ctx, ports.AsyncTaskUpdate{
			TenantID: task.TenantID, ID: task.ID, Status: "running",
			ProgressPct: pct, Result: map[string]any{"shares": plan.Shares, "last_message": message},
		}); err != nil {
			log.Printf("[GPU-PARTITION] progress update degraded: task_id=%s err=%v", task.ID, err)
		}
	}
	result, err := api.planner.ApplyGPUPartition(ctx, plan, progress)
	now := time.Now().UTC()
	if err != nil {
		if _, uerr := api.tasks.Update(ctx, ports.AsyncTaskUpdate{
			TenantID: task.TenantID, ID: task.ID, Status: "failed",
			AttemptCount: 1, ProgressPct: lastPct,
			Result: map[string]any{"shares": plan.Shares}, ErrorMessage: err.Error(), CompletedAt: now,
		}); uerr != nil {
			log.Printf("[GPU-PARTITION] failure update degraded: task_id=%s err=%v", task.ID, uerr)
		}
		return
	}
	resultMap := map[string]any{"shares": plan.Shares}
	if data, merr := json.Marshal(result); merr == nil {
		var decoded map[string]any
		if json.Unmarshal(data, &decoded) == nil {
			for k, v := range decoded {
				resultMap[k] = v
			}
		}
	}
	if _, uerr := api.tasks.Update(ctx, ports.AsyncTaskUpdate{
		TenantID: task.TenantID, ID: task.ID, Status: "completed",
		AttemptCount: 1, ProgressPct: 100, Result: resultMap, CompletedAt: now,
	}); uerr != nil {
		log.Printf("[GPU-PARTITION] completion update degraded: task_id=%s err=%v", task.ID, uerr)
	}
}

// resumeOrphanedTask re-enters the apply write sequence once for a running
// gpu_partition task that outlived gpuPartitionResumeAfter (gateway restart
// while applying). Re-entry is safe: every K8s step is idempotent and the
// terminal-status guard on AsyncTaskStore keeps a completed row terminal.
// All failures degrade silently to the stored snapshot.
func (api *gpuPartitionAPI) resumeOrphanedTask(ctx context.Context, tenantID string, task ports.AsyncTaskRecord) ports.AsyncTaskRecord {
	if api.planner == nil || time.Since(task.CreatedAt) < gpuPartitionResumeAfter {
		return task
	}
	sharesValue, ok := task.Result["shares"].(float64)
	if !ok || sharesValue < 2 || sharesValue > 8 {
		return task
	}
	if !api.startApply(task) {
		return task
	}
	plan, err := api.planner.PlanGPUPartition(ctx, int(sharesValue))
	if err != nil || len(plan.EligibleNodes) == 0 {
		// Cluster state is unknown/unusable; release the slot and degrade to
		// the stored snapshot instead of reporting a misleading failure.
		gpuPartitionAppliers.Delete(tenantID + "\x00" + task.ID)
		return task
	}
	go api.runApply(detachedTaskContext(task), task, plan)
	return task
}
