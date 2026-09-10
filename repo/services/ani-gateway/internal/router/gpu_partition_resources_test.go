package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/protocol"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// fakePartitionPlanner records plan/apply invocations and lets tests gate the
// apply goroutine deterministically.
type fakePartitionPlanner struct {
	plan       ports.GPUPartitionPlan
	planErr    error
	applyErr   error
	applyCalls atomic.Int32
	planCalls  atomic.Int32
	blockApply chan struct{}
}

func (f *fakePartitionPlanner) PlanGPUPartition(ctx context.Context, shares int) (ports.GPUPartitionPlan, error) {
	f.planCalls.Add(1)
	if f.planErr != nil {
		return ports.GPUPartitionPlan{}, f.planErr
	}
	switch shares {
	case 2, 4, 8:
		return f.plan, nil
	default:
		return ports.GPUPartitionPlan{}, fmt.Errorf("%w: shares must be one of 2, 4, 8", ports.ErrInvalid)
	}
}

func (f *fakePartitionPlanner) ApplyGPUPartition(ctx context.Context, plan ports.GPUPartitionPlan, progress ports.GPUPartitionProgressFunc) (ports.GPUPartitionApplyResult, error) {
	f.applyCalls.Add(1)
	if progress != nil {
		progress(1, 2, "fake stage")
	}
	if f.blockApply != nil {
		<-f.blockApply
	}
	if f.applyErr != nil {
		return ports.GPUPartitionApplyResult{}, f.applyErr
	}
	return ports.GPUPartitionApplyResult{
		AppliedNodes: []string{"node-1"},
		SkippedNodes: []ports.GPUPartitionSkippedNode{},
		FailedNodes:  []ports.GPUPartitionNodeResult{},
	}, nil
}

func idlePlan(shares int) ports.GPUPartitionPlan {
	return ports.GPUPartitionPlan{
		Shares: shares,
		EligibleNodes: []ports.GPUPartitionCandidateNode{{
			NodeName: "node-1", Model: "NVIDIA-RTX-4090", CardCount: 2,
			TotalMemoryMiB: 24564, MBPerShare: 24564 / int64(shares),
			SharingSpec: fmt.Sprintf("NVIDIA-RTX-4090-%dMiB", 24564/shares),
		}},
		SkippedNodes: []ports.GPUPartitionSkippedNode{{NodeName: "node-busy", Reason: ports.GPUPartitionSkipBusy}},
	}
}

func newGPUPartitionTestServer(t *testing.T, planner ports.GPUPartitionPlanner, tasks ports.AsyncTaskStore) *server.Hertz {
	t.Helper()
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	registerGPUPartitionResources(h.Group("/api/v1"), planner, tasks)
	registerTasksWithStore(h.Group("/api/v1"), tasks, nil)
	return h
}

func gpuPartitionPost(t *testing.T, h *server.Hertz, body string, headers ...ut.Header) *protocol.Response {
	t.Helper()
	all := append([]ut.Header{{Key: "Content-Type", Value: "application/json"}}, headers...)
	return ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/gpu-inventory/gpu-partitions", &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, all...).Result()
}

func waitForTaskStatus(t *testing.T, tasks ports.AsyncTaskStore, taskID, status string) ports.AsyncTaskRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task, err := tasks.Get(context.Background(), "tenant-a", taskID)
		if err == nil && task.Status == status {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, err := tasks.Get(context.Background(), "tenant-a", taskID)
	if err != nil {
		t.Fatalf("task %s not found while waiting for %s: %v", taskID, status, err)
	}
	t.Fatalf("task %s status = %s, want %s", taskID, task.Status, status)
	return task
}

func TestGPUPartitionCreateAcceptsAndCompletes(t *testing.T) {
	planner := &fakePartitionPlanner{plan: idlePlan(4)}
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := newGPUPartitionTestServer(t, planner, tasks)

	resp := gpuPartitionPost(t, h, `{"shares":4}`, ut.Header{Key: "Idempotency-Key", Value: "partition-create-1"})
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("status = %d body=%s, want 202", resp.StatusCode(), resp.Body())
	}
	var accepted map[string]any
	if err := json.Unmarshal(resp.Body(), &accepted); err != nil {
		t.Fatalf("decode 202 body: %v", err)
	}
	taskID, _ := accepted["task_id"].(string)
	if taskID == "" {
		t.Fatalf("task_id missing in 202 body %s", resp.Body())
	}
	if accepted["task_type"] != gpuPartitionTaskType || accepted["status"] != "running" {
		t.Fatalf("accepted body = %s, want running gpu_partition task", resp.Body())
	}
	if got := string(resp.Header.Get("Location")); got != "/api/v1/tasks/"+taskID {
		t.Fatalf("Location = %q, want /api/v1/tasks/%s", got, taskID)
	}
	task := waitForTaskStatus(t, tasks, taskID, "completed")
	if task.ProgressPct != 100 {
		t.Fatalf("progress = %d, want 100", task.ProgressPct)
	}
	applied, ok := task.Result["applied_nodes"].([]any)
	if !ok || len(applied) != 1 || applied[0] != "node-1" {
		t.Fatalf("result = %v, want applied_nodes [node-1]", task.Result)
	}
	if task.Result["shares"].(float64) != 4 {
		t.Fatalf("result shares = %v, want 4", task.Result["shares"])
	}
}

func TestGPUPartitionCreateNoIdleNodesReturns422(t *testing.T) {
	planner := &fakePartitionPlanner{plan: ports.GPUPartitionPlan{Shares: 2, SkippedNodes: []ports.GPUPartitionSkippedNode{{NodeName: "node-busy", Reason: ports.GPUPartitionSkipBusy}}}}
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := newGPUPartitionTestServer(t, planner, tasks)

	resp := gpuPartitionPost(t, h, `{"shares":2}`, ut.Header{Key: "Idempotency-Key", Value: "partition-none"})
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s, want 422", resp.StatusCode(), resp.Body())
	}
	if got := jsonStringField(t, resp.Body(), "code"); got != "NO_IDLE_WHOLECARD_GPUS" {
		t.Fatalf("code = %q, want NO_IDLE_WHOLECARD_GPUS", got)
	}
	if planner.applyCalls.Load() != 0 {
		t.Fatalf("apply called %d times, want 0", planner.applyCalls.Load())
	}
}

func TestGPUPartitionCreateValidation(t *testing.T) {
	planner := &fakePartitionPlanner{plan: idlePlan(2)}
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := newGPUPartitionTestServer(t, planner, tasks)

	if resp := gpuPartitionPost(t, h, `{"shares":2}`); resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key status = %d, want 400", resp.StatusCode())
	}
	if resp := gpuPartitionPost(t, h, `{"shares":3}`, ut.Header{Key: "Idempotency-Key", Value: "k1"}); resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("shares=3 status = %d body=%s, want 400", resp.StatusCode(), resp.Body())
	}
}

func TestGPUPartitionCreatePlannerNotConfigured(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := newGPUPartitionTestServer(t, nil, tasks)
	resp := gpuPartitionPost(t, h, `{"shares":2}`, ut.Header{Key: "Idempotency-Key", Value: "k2"})
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s, want 503", resp.StatusCode(), resp.Body())
	}
	if got := jsonStringField(t, resp.Body(), "code"); got != "NOT_CONFIGURED" {
		t.Fatalf("code = %q, want NOT_CONFIGURED", got)
	}
}

func TestGPUPartitionIdempotentReplayReusesTask(t *testing.T) {
	planner := &fakePartitionPlanner{plan: idlePlan(8)}
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := newGPUPartitionTestServer(t, planner, tasks)

	first := gpuPartitionPost(t, h, `{"shares":8}`, ut.Header{Key: "Idempotency-Key", Value: "partition-replay"})
	if first.StatusCode() != http.StatusAccepted {
		t.Fatalf("first status = %d body=%s, want 202", first.StatusCode(), first.Body())
	}
	second := gpuPartitionPost(t, h, `{"shares":8}`, ut.Header{Key: "Idempotency-Key", Value: "partition-replay"})
	if second.StatusCode() != http.StatusAccepted {
		t.Fatalf("second status = %d body=%s, want 202", second.StatusCode(), second.Body())
	}
	firstID := jsonStringField(t, first.Body(), "task_id")
	secondID := jsonStringField(t, second.Body(), "task_id")
	if firstID == "" || firstID != secondID {
		t.Fatalf("task ids = %q vs %q, want identical replay", firstID, secondID)
	}
	waitForTaskStatus(t, tasks, firstID, "completed")
	if calls := planner.applyCalls.Load(); calls != 1 {
		t.Fatalf("apply calls = %d, want 1 (replay must not re-execute)", calls)
	}
}

func TestGPUPartitionApplyFailureMarksTaskFailed(t *testing.T) {
	planner := &fakePartitionPlanner{plan: idlePlan(2), applyErr: fmt.Errorf("boom")}
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := newGPUPartitionTestServer(t, planner, tasks)

	resp := gpuPartitionPost(t, h, `{"shares":2}`, ut.Header{Key: "Idempotency-Key", Value: "partition-fail"})
	taskID := jsonStringField(t, resp.Body(), "task_id")
	task := waitForTaskStatus(t, tasks, taskID, "failed")
	if task.ErrorMessage != "boom" {
		t.Fatalf("error message = %q, want boom", task.ErrorMessage)
	}
}

func TestGPUPartitionLazyResumeReentersOrphanedTask(t *testing.T) {
	planner := &fakePartitionPlanner{plan: idlePlan(2)}
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := newGPUPartitionTestServer(t, planner, tasks)

	// Simulate an orphaned task left behind by a gateway restart mid-apply:
	// running, old CreatedAt, shares stored in the result.
	created, _, err := tasks.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID:       "tenant-a",
		IdempotencyKey: "partition-orphan",
		TaskType:       gpuPartitionTaskType,
		ResourceType:   gpuPartitionResourceType,
		Status:         "running",
		AttemptCount:   1,
		MaxAttempts:    1,
		Result:         map[string]any{"shares": 2},
		CreatedAt:      time.Now().Add(-gpuPartitionResumeAfter - time.Minute).UTC(),
	})
	if err != nil {
		t.Fatalf("seed orphan task: %v", err)
	}

	req := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/tasks/"+created.ID, nil).Result()
	if req.StatusCode() != http.StatusOK {
		t.Fatalf("get orphan status = %d body=%s, want 200", req.StatusCode(), req.Body())
	}
	task := waitForTaskStatus(t, tasks, created.ID, "completed")
	if planner.applyCalls.Load() != 1 {
		t.Fatalf("apply calls = %d, want 1 after lazy-resume", planner.applyCalls.Load())
	}
	applied, ok := task.Result["applied_nodes"].([]any)
	if !ok || len(applied) != 1 || applied[0] != "node-1" {
		t.Fatalf("resumed result = %v, want applied_nodes [node-1]", task.Result)
	}
}

func TestGPUPartitionFreshRunningTaskNotResumed(t *testing.T) {
	planner := &fakePartitionPlanner{plan: idlePlan(2)}
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := newGPUPartitionTestServer(t, planner, tasks)

	created, _, err := tasks.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID:       "tenant-a",
		IdempotencyKey: "partition-fresh",
		TaskType:       gpuPartitionTaskType,
		ResourceType:   gpuPartitionResourceType,
		Status:         "running",
		AttemptCount:   1,
		MaxAttempts:    1,
		Result:         map[string]any{"shares": 2},
	})
	if err != nil {
		t.Fatalf("seed fresh task: %v", err)
	}
	req := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/tasks/"+created.ID, nil).Result()
	if req.StatusCode() != http.StatusOK {
		t.Fatalf("get status = %d, want 200", req.StatusCode())
	}
	time.Sleep(50 * time.Millisecond)
	if calls := planner.applyCalls.Load(); calls != 0 {
		t.Fatalf("apply calls = %d, want 0 (fresh task must not be resumed)", calls)
	}
}
