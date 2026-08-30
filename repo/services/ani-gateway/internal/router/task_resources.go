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

var defaultTaskStore ports.AsyncTaskStore = runtimeadapter.NewLocalAsyncTaskStore()

type taskAPI struct {
	store ports.AsyncTaskStore
}

func registerTasksWithStore(v1 *route.RouterGroup, store ports.AsyncTaskStore) {
	if store == nil {
		store = defaultTaskStore
	}
	api := &taskAPI{store: store}
	v1.GET("/tasks/:task_id", api.get)
}

func (api *taskAPI) get(ctx context.Context, c *app.RequestContext) {
	task, err := api.store.Get(ctx, instanceTenantID(c), c.Param("task_id"))
	if errors.Is(err, ports.ErrNotFound) {
		writeInstanceError(c, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}
	if err != nil {
		writeInstanceError(c, http.StatusInternalServerError, "TASK_LOOKUP_FAILED", err.Error())
		return
	}
	c.JSON(http.StatusOK, taskResponseFromRecord(task))
}

func taskResponseFromRecord(record ports.AsyncTaskRecord) storageSnapshotTaskResponse {
	return storageSnapshotTaskResponse{
		ID: record.ID, IdempotencyKey: record.IdempotencyKey, TaskType: record.TaskType,
		ResourceType: record.ResourceType, Status: record.Status, AttemptCount: record.AttemptCount,
		MaxAttempts: record.MaxAttempts, ProgressPct: record.ProgressPct, Result: record.Result,
		CreatedAt: networkTime(record.CreatedAt), CompletedAt: networkTime(record.CompletedAt),
	}
}

func taskRecordFromResponse(tenantID string, task storageSnapshotTaskResponse) ports.AsyncTaskRecord {
	createdAt, _ := timeFromNetwork(task.CreatedAt)
	completedAt, _ := timeFromNetwork(task.CompletedAt)
	return ports.AsyncTaskRecord{
		TenantID: tenantID, ID: task.ID, IdempotencyKey: task.IdempotencyKey,
		TaskType: task.TaskType, ResourceType: task.ResourceType, Status: task.Status,
		AttemptCount: task.AttemptCount, MaxAttempts: task.MaxAttempts, ProgressPct: task.ProgressPct,
		Result: task.Result, CreatedAt: createdAt, CompletedAt: completedAt,
	}
}

func timeFromNetwork(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}
