package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

type taskAPI struct {
	service ports.AsyncTaskService
}

func registerTasks(v1 *route.RouterGroup) {
	registerTasksWithService(v1, nil)
}

func registerTasksWithService(v1 *route.RouterGroup, service ports.AsyncTaskService) {
	api := newTaskAPI(service)
	v1.GET("/tasks/:task_id", api.getTask)
	v1.DELETE("/tasks/:task_id", api.cancelTask)
}

func newTaskAPI(service ports.AsyncTaskService) *taskAPI {
	if service == nil {
		service = runtimeadapter.NewLocalAsyncTaskService()
	}
	return &taskAPI{service: service}
}

func (api *taskAPI) getTask(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.GetTask(ctx, middleware.GetTenantID(c), c.Param("task_id"))
	if err != nil {
		writeTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, asyncTaskResponseFromRecord(record))
}

func (api *taskAPI) cancelTask(ctx context.Context, c *app.RequestContext) {
	record, err := api.service.CancelTask(ctx, middleware.GetTenantID(c), c.Param("task_id"))
	if err != nil {
		writeTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, asyncTaskResponseFromRecord(record))
}

func asyncTaskResponseFromRecord(record ports.AsyncTaskRecord) map[string]any {
	response := map[string]any{
		"id":              record.ID,
		"idempotency_key": record.IdempotencyKey,
		"task_type":       record.TaskType,
		"status":          record.Status,
		"attempt_count":   record.AttemptCount,
		"max_attempts":    record.MaxAttempts,
		"progress_pct":    record.ProgressPct,
		"created_at":      networkTime(record.CreatedAt),
	}
	if record.ResourceType != "" {
		response["resource_type"] = record.ResourceType
	}
	if record.ResourceID != "" {
		response["resource_id"] = record.ResourceID
	}
	if len(record.Result) > 0 {
		var result map[string]any
		if err := json.Unmarshal(record.Result, &result); err == nil {
			response["result"] = result
		}
	}
	if record.ErrorMessage != "" {
		response["error_message"] = record.ErrorMessage
	}
	if record.DeadLetterAt != nil {
		response["dead_letter_at"] = networkTime(*record.DeadLetterAt)
	}
	if record.CompletedAt != nil {
		response["completed_at"] = networkTime(*record.CompletedAt)
	}
	return response
}

func writeTaskError(c *app.RequestContext, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeDemoError(c, http.StatusNotFound, "TASK_NOT_FOUND", err.Error())
	case errors.Is(err, ports.ErrInvalid):
		writeDemoError(c, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	default:
		writeDemoError(c, http.StatusInternalServerError, "TASK_LOOKUP_FAILED", err.Error())
	}
}
