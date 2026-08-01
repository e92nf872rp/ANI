package router

import (
	"context"
	"net/http"
	"sync"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

var completedTasks = struct {
	sync.RWMutex
	byTenant map[string]map[string]storageSnapshotTaskResponse
}{
	byTenant: make(map[string]map[string]storageSnapshotTaskResponse),
}

func registerTasks(v1 *route.RouterGroup) {
	v1.GET("/tasks/:task_id", getTask)
	v1.DELETE("/tasks/:task_id", cancelTask)
}

func getTask(ctx context.Context, c *app.RequestContext) {
	tenantID := instanceTenantID(c)
	completedTasks.RLock()
	task, ok := completedTasks.byTenant[tenantID][c.Param("task_id")]
	completedTasks.RUnlock()
	if !ok {
		writeInstanceError(c, http.StatusNotFound, "NOT_FOUND", "task not found")
		return
	}
	c.JSON(http.StatusOK, task)
	_ = ctx
}

func storeCompletedTask(tenantID string, task storageSnapshotTaskResponse) {
	completedTasks.Lock()
	defer completedTasks.Unlock()
	if completedTasks.byTenant[tenantID] == nil {
		completedTasks.byTenant[tenantID] = make(map[string]storageSnapshotTaskResponse)
	}
	completedTasks.byTenant[tenantID][task.ID] = task
}

func cancelTask(ctx context.Context, c *app.RequestContext) {
	_ = middleware.GetTenantID(c)
	c.Status(http.StatusNoContent)
	_ = ctx
}
