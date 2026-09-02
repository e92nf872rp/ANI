package router

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

var defaultTaskStore ports.AsyncTaskStore = runtimeadapter.NewLocalAsyncTaskStore()

const (
	defaultTaskListLimit = 20
	maxTaskListLimit     = 100
)

// instanceObserver returns the freshest view of a single instance: a store
// read followed by a live Kubernetes refresh. It is the lazy-sync
// observation entry shared between the instance API and the task API.
type instanceObserver func(ctx context.Context, tenantID, instanceID string) (ports.WorkloadInstanceRecord, error)

type taskAPI struct {
	store           ports.AsyncTaskStore
	observeInstance instanceObserver
}

func registerTasksWithStore(v1 *route.RouterGroup, store ports.AsyncTaskStore, observeInstance instanceObserver) {
	if store == nil {
		store = defaultTaskStore
	}
	api := &taskAPI{store: store, observeInstance: observeInstance}
	v1.GET("/tasks", api.list)
	v1.GET("/tasks/:task_id", api.get)
}

type taskListResponse struct {
	Items      []storageSnapshotTaskResponse `json:"items"`
	NextCursor string                        `json:"next_cursor"`
}

func (api *taskAPI) list(ctx context.Context, c *app.RequestContext) {
	filter := ports.AsyncTaskListFilter{
		Status:       strings.TrimSpace(c.Query("status")),
		TaskType:     strings.TrimSpace(c.Query("task_type")),
		ResourceType: strings.TrimSpace(c.Query("resource_type")),
		Cursor:       strings.TrimSpace(c.Query("cursor")),
		Limit:        defaultTaskListLimit,
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxTaskListLimit {
			writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", "limit must be between 1 and 100")
			return
		}
		filter.Limit = limit
	}
	// List returns stored snapshots without lazy sync: refreshing N running
	// tasks would mean N Kubernetes round trips per request.
	tasks, nextCursor, err := api.store.List(ctx, instanceTenantID(c), filter)
	if errors.Is(err, ports.ErrInvalid) {
		writeInstanceError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "TASK_LIST_FAILED", err.Error())
		return
	}
	items := make([]storageSnapshotTaskResponse, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, taskResponseFromRecord(task))
	}
	c.JSON(http.StatusOK, taskListResponse{Items: items, NextCursor: nextCursor})
}

func (api *taskAPI) get(ctx context.Context, c *app.RequestContext) {
	tenantID := instanceTenantID(c)
	task, err := api.store.Get(ctx, tenantID, c.Param("task_id"))
	if errors.Is(err, ports.ErrNotFound) {
		writeInstanceError(c, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "TASK_LOOKUP_FAILED", err.Error())
		return
	}
	if task.ResourceType == "instance" && task.Status == "running" && api.observeInstance != nil {
		if synced, syncErr := api.syncInstanceTask(ctx, tenantID, task); syncErr == nil {
			task = synced
		} else {
			// Lazy sync is best-effort: degrade to the stored snapshot
			// instead of failing the read.
			log.Printf("[TASK-SYNC] lazy sync degraded: task_id=%s err=%v", task.ID, syncErr)
		}
	}
	c.JSON(http.StatusOK, taskResponseFromRecord(task))
}

// syncInstanceTask advances a running instance task by observing the live
// instance and applying the state mapping table (migration plan §5.2).
func (api *taskAPI) syncInstanceTask(ctx context.Context, tenantID string, task ports.AsyncTaskRecord) (ports.AsyncTaskRecord, error) {
	action := strings.TrimPrefix(task.TaskType, "instance.")
	switch action {
	case "create", "start", "restart", "stop", "delete":
	default:
		return task, nil
	}
	inst, err := api.observeInstance(ctx, tenantID, instanceIDForTask(task))
	next, terminal, err := instanceTaskAdvance(action, err, inst, task)
	if err != nil {
		return task, err
	}
	if !terminal && next.ProgressPct == task.ProgressPct && next.Status == task.Status {
		// State and progress unchanged: skip the write to avoid amplification.
		return task, nil
	}
	next.TenantID, next.ID = tenantID, task.ID
	updated, uerr := api.store.Update(ctx, next)
	if uerr != nil {
		return task, uerr
	}
	return updated, nil
}

// instanceIDForTask resolves the observation handle for an instance task.
// resource_id stores the UUID part of the "inst_<uuid>" instance ID (the
// contract and async_tasks.resource_id column are UUID-typed), while
// result.instance_id carries the full instance ID used for store lookups.
func instanceIDForTask(task ports.AsyncTaskRecord) string {
	if value, ok := task.Result["instance_id"].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return task.ResourceID
}

// instanceTaskAdvance maps the observed instance state onto the target task
// update. Terminal targets are completed/failed rows; running targets keep
// the task open. Observation errors other than ErrNotFound are transient and
// returned for caller-side degradation; a vanished instance record is a
// mapping input (the "gone" state).
func instanceTaskAdvance(action string, observeErr error, inst ports.WorkloadInstanceRecord, current ports.AsyncTaskRecord) (ports.AsyncTaskUpdate, bool, error) {
	if observeErr != nil && !errors.Is(observeErr, ports.ErrNotFound) {
		return ports.AsyncTaskUpdate{}, false, observeErr
	}
	state := "gone"
	if observeErr == nil {
		state = string(inst.Status.State)
	}
	var status, errorMessage string
	var progress int
	terminal := true
	switch action {
	case "delete":
		if state == "deleted" || state == "gone" {
			status, progress = "completed", 100
		} else {
			status, progress, terminal = "running", 80, false
		}
	case "stop":
		switch state {
		case "stopped":
			status, progress = "completed", 100
		case "failed":
			status, progress, errorMessage = "failed", current.ProgressPct, "instance entered failed state"
		case "stopping", "deleting":
			status, progress, terminal = "running", 60, false
		case "deleted":
			status, progress, errorMessage = "failed", current.ProgressPct, "instance deleted before reaching stopped"
		case "gone":
			status, progress, errorMessage = "failed", current.ProgressPct, "instance record not found"
		default: // provisioning, pending, starting, running
			status, progress, terminal = "running", 30, false
		}
	default: // create, start, restart
		switch state {
		case "running":
			status, progress = "completed", 100
		case "failed":
			status, progress, errorMessage = "failed", current.ProgressPct, "instance entered failed state"
		case "provisioning", "pending":
			status, progress, terminal = "running", 20, false
		case "deleted":
			status, progress, errorMessage = "failed", current.ProgressPct, "instance deleted before reaching running"
		case "gone":
			status, progress, errorMessage = "failed", current.ProgressPct, "instance record not found"
		default: // starting, stopping, stopped, deleting
			status, progress, terminal = "running", 40, false
		}
	}
	result := make(map[string]any, len(current.Result)+1)
	for key, value := range current.Result {
		result[key] = value
	}
	if observeErr == nil {
		result["state"] = string(inst.Status.State)
	}
	update := ports.AsyncTaskUpdate{
		TenantID:     current.TenantID,
		ID:           current.ID,
		Status:       status,
		AttemptCount: current.AttemptCount,
		ProgressPct:  progress,
		Result:       result,
		ErrorMessage: errorMessage,
	}
	if status == "completed" {
		update.CompletedAt = time.Now().UTC()
	}
	return update, terminal, nil
}

func taskResponseFromRecord(record ports.AsyncTaskRecord) storageSnapshotTaskResponse {
	return storageSnapshotTaskResponse{
		ID: record.ID, IdempotencyKey: record.IdempotencyKey, TaskType: record.TaskType,
		ResourceType: record.ResourceType, ResourceID: record.ResourceID, Status: record.Status,
		AttemptCount: record.AttemptCount, MaxAttempts: record.MaxAttempts, ProgressPct: record.ProgressPct,
		Result: record.Result, ErrorMessage: record.ErrorMessage, DeadLetterAt: networkTime(record.DeadLetterAt),
		CreatedAt: networkTime(record.CreatedAt), CompletedAt: networkTime(record.CompletedAt),
	}
}

func taskRecordFromResponse(tenantID string, task storageSnapshotTaskResponse) ports.AsyncTaskRecord {
	createdAt, _ := timeFromNetwork(task.CreatedAt)
	completedAt, _ := timeFromNetwork(task.CompletedAt)
	deadLetterAt, _ := timeFromNetwork(task.DeadLetterAt)
	return ports.AsyncTaskRecord{
		TenantID: tenantID, ID: task.ID, IdempotencyKey: task.IdempotencyKey,
		TaskType: task.TaskType, ResourceType: task.ResourceType, ResourceID: task.ResourceID,
		Status: task.Status, AttemptCount: task.AttemptCount, MaxAttempts: task.MaxAttempts,
		ProgressPct: task.ProgressPct, Result: task.Result, ErrorMessage: task.ErrorMessage,
		DeadLetterAt: deadLetterAt, CreatedAt: createdAt, CompletedAt: completedAt,
	}
}

func timeFromNetwork(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}
