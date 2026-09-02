package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// taskTestHarness wires an instance runtime plus task routes on one Hertz
// server so the write points and the lazy-sync observer can be exercised
// end-to-end over HTTP.
type taskTestHarness struct {
	h        *server.Hertz
	tasks    ports.AsyncTaskStore
	injected *instanceAPI
	tenantID string
}

func newTaskTestHarness(t *testing.T, tasks ports.AsyncTaskStore) *taskTestHarness {
	t.Helper()
	return newTaskTestHarnessWithK8s(t, tasks, nil)
}

func newTaskTestHarnessWithK8s(t *testing.T, tasks ports.AsyncTaskStore, k8sClient *runtimeadapter.KubernetesRESTClient) *taskTestHarness {
	t.Helper()
	injected := newInstanceAPI()
	runtime := &InstanceRuntime{
		Service:    injected.service,
		Store:      injected.store,
		Operations: injected.operations,
		TaskStore:  tasks,
	}
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Set("user_id", "user-a")
		c.Next(ctx)
	})
	v1 := h.Group("/api/v1")
	_, observer := registerInstancesWithRuntime(v1, nil, false, nil, k8sClient, nil, runtime, nil)
	registerTasksWithStore(v1, tasks, observer)
	return &taskTestHarness{h: h, tasks: tasks, injected: injected, tenantID: "tenant-a"}
}

func (th *taskTestHarness) createGPUContainer(t *testing.T, idempotencyKey string) string {
	t.Helper()
	body := fmt.Sprintf(`{"kind":"gpu_container","name":"gpu-%s","idempotency_key":"%s","cpu":"2","memory":"4Gi"}`, idempotencyKey, idempotencyKey)
	created := performJSONRequest(t, th.h, http.MethodPost, "/api/v1/instances", body, http.StatusCreated)
	instance := decodedObject(t, created, "instance")
	instanceID, _ := instance["id"].(string)
	if instanceID == "" || !strings.HasPrefix(instanceID, "inst_") {
		t.Fatalf("create instance id = %q, want inst_ prefixed id in %s", instanceID, created)
	}
	state, _ := instance["state"].(string)
	if state != "running" {
		t.Fatalf("create instance state = %q, want running", state)
	}
	// The instance response contract is untouched by the task write point.
	top := decodedObject(t, created, "")
	for _, forbidden := range []string{"task", "next_cursor", "task_type"} {
		if _, present := top[forbidden]; present {
			t.Fatalf("create response must not gain task field %q: %s", forbidden, created)
		}
	}
	return instanceID
}

func (th *taskTestHarness) lifecycle(t *testing.T, instanceID, action, idempotencyKey string) []byte {
	t.Helper()
	body := fmt.Sprintf(`{"action":"%s","idempotency_key":"%s"}`, action, idempotencyKey)
	return performJSONRequest(t, th.h, http.MethodPost, "/api/v1/instances/"+instanceID+"/lifecycle", body, http.StatusOK)
}

func (th *taskTestHarness) getTask(t *testing.T, taskID string) map[string]any {
	t.Helper()
	body := performJSONRequest(t, th.h, http.MethodGet, "/api/v1/tasks/"+taskID, "", http.StatusOK)
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode task body: %v", err)
	}
	return decoded
}

func (th *taskTestHarness) listTasks(t *testing.T, query string) map[string]any {
	t.Helper()
	body := performJSONRequest(t, th.h, http.MethodGet, "/api/v1/tasks"+query, "", http.StatusOK)
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode task list body: %v", err)
	}
	return decoded
}

func (th *taskTestHarness) singleTaskByType(t *testing.T, taskType string) ports.AsyncTaskRecord {
	t.Helper()
	records, _, err := th.tasks.List(context.Background(), th.tenantID, ports.AsyncTaskListFilter{TaskType: taskType, Limit: 10})
	if err != nil {
		t.Fatalf("List(%s) error = %v", taskType, err)
	}
	if len(records) != 1 {
		t.Fatalf("List(%s) = %d records, want exactly 1", taskType, len(records))
	}
	return records[0]
}

func (th *taskTestHarness) setInstanceState(t *testing.T, instanceID string, state ports.WorkloadState) {
	t.Helper()
	record, err := th.injected.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   th.tenantID,
		InstanceID: instanceID,
	})
	if err != nil {
		t.Fatalf("service.Get(%s) error = %v", instanceID, err)
	}
	record.Status.State = state
	record.Status.UpdatedAt = time.Now().UTC()
	if err := th.injected.store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatalf("UpsertStatus(%s) error = %v", instanceID, err)
	}
}

func decodedObject(t *testing.T, body []byte, key string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if key == "" {
		return decoded
	}
	value, _ := decoded[key].(map[string]any)
	return value
}

// --- §8.2 list endpoint: ordering, limits, cursor, filters ---

func seedTaskListStore(t *testing.T, tenantID string) *runtimeadapter.LocalAsyncTaskStore {
	t.Helper()
	store := runtimeadapter.NewLocalAsyncTaskStore()
	createdAt := time.Unix(5000, 0).UTC()
	specs := []struct {
		id, key, taskType, resourceType, status string
		offsetSeconds                           int
	}{
		{"11111111-1111-4111-8111-111111111111", "http-list-a", "instance.create", "instance", "running", 0},
		{"22222222-2222-4222-8222-222222222222", "http-list-b", "instance.delete", "instance", "completed", 10},
		// Same created_at as http-list-b: id DESC is the tiebreaker.
		{"33333333-3333-4333-8333-333333333333", "http-list-c", "kb.parse", "kb_document", "pending", 10},
		{"44444444-4444-4444-8444-444444444444", "http-list-d", "volume.expand", "volume", "completed", 30},
		{"55555555-5555-4555-8555-555555555555", "http-list-e", "instance.start", "instance", "failed", 40},
	}
	for _, spec := range specs {
		if _, _, err := store.Create(context.Background(), ports.AsyncTaskRecord{
			TenantID: tenantID, ID: spec.id, IdempotencyKey: spec.key,
			TaskType: spec.taskType, ResourceType: spec.resourceType, Status: spec.status,
			MaxAttempts: 1, ProgressPct: 10,
			Result:    map[string]any{"seed": true},
			CreatedAt: createdAt.Add(time.Duration(spec.offsetSeconds) * time.Second),
		}); err != nil {
			t.Fatalf("seed Create(%s) error = %v", spec.key, err)
		}
	}
	return store
}

func TestTaskListHTTPEndpoint(t *testing.T) {
	tasks := seedTaskListStore(t, "tenant-a")
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	registerTasksWithStore(h.Group("/api/v1"), tasks, nil)

	// Default ordering: created_at DESC then id DESC, including the tie pair.
	body := performJSONRequest(t, h, http.MethodGet, "/api/v1/tasks", "", http.StatusOK)
	decoded := decodedObject(t, body, "")
	items, _ := decoded["items"].([]any)
	wantOrder := []string{
		"55555555-5555-4555-8555-555555555555",
		"44444444-4444-4444-8444-444444444444",
		"33333333-3333-4333-8333-333333333333",
		"22222222-2222-4222-8222-222222222222",
		"11111111-1111-4111-8111-111111111111",
	}
	if len(items) != len(wantOrder) {
		t.Fatalf("list items = %d, want %d", len(items), len(wantOrder))
	}
	for i, want := range wantOrder {
		item, _ := items[i].(map[string]any)
		if item["id"] != want {
			t.Fatalf("items[%d].id = %v, want %s", i, item["id"], want)
		}
	}
	if next, _ := decoded["next_cursor"].(string); next != "" {
		t.Fatalf("next_cursor = %q, want empty on last page", next)
	}

	// Cursor pagination walks every row exactly once.
	seen := make([]string, 0, 5)
	cursor := ""
	pages := 0
	for {
		path := "/api/v1/tasks?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		page := decodedObject(t, performJSONRequest(t, h, http.MethodGet, path, "", http.StatusOK), "")
		pageItems, _ := page["items"].([]any)
		for _, raw := range pageItems {
			item, _ := raw.(map[string]any)
			seen = append(seen, item["id"].(string))
		}
		pages++
		next, _ := page["next_cursor"].(string)
		if next == "" {
			break
		}
		cursor = next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pages != 3 || len(seen) != 5 {
		t.Fatalf("pagination = (%d pages, %d ids), want (3, 5): %v", pages, len(seen), seen)
	}
	for i, want := range wantOrder {
		if seen[i] != want {
			t.Fatalf("paginated ids[%d] = %s, want %s", i, seen[i], want)
		}
	}

	// Filters.
	filterTests := []struct {
		query, wantFirst string
		wantCount        int
	}{
		{"?status=completed", "44444444-4444-4444-8444-444444444444", 2},
		{"?task_type=instance.create", "11111111-1111-4111-8111-111111111111", 1},
		{"?resource_type=instance", "55555555-5555-4555-8555-555555555555", 3},
		{"?task_type=inference.deploy", "", 0},
		{"?resource_type=model_version", "", 0},
	}
	for _, test := range filterTests {
		filtered := decodedObject(t, performJSONRequest(t, h, http.MethodGet, "/api/v1/tasks"+test.query, "", http.StatusOK), "")
		items, _ := filtered["items"].([]any)
		if len(items) != test.wantCount {
			t.Fatalf("list %s = %d items, want %d", test.query, len(items), test.wantCount)
		}
		if test.wantFirst != "" {
			item, _ := items[0].(map[string]any)
			if item["id"] != test.wantFirst {
				t.Fatalf("list %s first = %v, want %s", test.query, item["id"], test.wantFirst)
			}
		}
	}

	// Limit boundaries: 1 and 100 are valid.
	for _, limit := range []string{"1", "100"} {
		limited := decodedObject(t, performJSONRequest(t, h, http.MethodGet, "/api/v1/tasks?limit="+limit, "", http.StatusOK), "")
		if _, ok := limited["items"]; !ok {
			t.Fatalf("limit=%s response missing items", limit)
		}
	}
	// Out-of-range and non-numeric limits are rejected.
	for _, limit := range []string{"0", "101", "abc"} {
		resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/tasks?limit="+limit, nil).Result()
		if resp.StatusCode() != http.StatusBadRequest {
			t.Fatalf("limit=%s status = %d, want 400", limit, resp.StatusCode())
		}
	}
	// Invalid status enum and invalid cursor are rejected.
	for _, query := range []string{"?status=succeeded", "?cursor=not-base64!!!", "?cursor=aGVsbG8"} {
		resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/tasks"+query, nil).Result()
		if resp.StatusCode() != http.StatusBadRequest {
			t.Fatalf("list %s status = %d, want 400 (body=%s)", query, resp.StatusCode(), resp.Body())
		}
	}
}

func TestTaskListAndGetCrossTenantIsolation(t *testing.T) {
	tasks := seedTaskListStore(t, "tenant-a")
	other := server.New()
	other.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-b")
		c.Next(ctx)
	})
	registerTasksWithStore(other.Group("/api/v1"), tasks, nil)

	otherList := decodedObject(t, performJSONRequest(t, other, http.MethodGet, "/api/v1/tasks", "", http.StatusOK), "")
	items, _ := otherList["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("tenant-b list = %d items, want 0", len(items))
	}
	resp := ut.PerformRequest(other.Engine, http.MethodGet, "/api/v1/tasks/22222222-2222-4222-8222-222222222222", nil).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("tenant-b get = %d, want 404", resp.StatusCode())
	}
}

// --- §8.2 instance write points ---

func TestInstanceCreateWritesRunningTask(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	instanceID := harness.createGPUContainer(t, "task-create-write")

	record := harness.singleTaskByType(t, "instance.create")
	if record.Status != "running" || record.ProgressPct != 10 {
		t.Fatalf("instance.create task = (%s, %d), want (running, 10)", record.Status, record.ProgressPct)
	}
	if record.ResourceType != "instance" {
		t.Fatalf("resource_type = %q, want instance", record.ResourceType)
	}
	if record.IdempotencyKey != "task-create-write" {
		t.Fatalf("idempotency_key = %q, want the request key", record.IdempotencyKey)
	}
	// resource_id carries the UUID part of inst_<uuid> for the UUID-typed
	// column; the full instance ID survives in result.instance_id.
	if record.ResourceID == "" || strings.HasPrefix(record.ResourceID, "inst_") {
		t.Fatalf("resource_id = %q, want bare UUID", record.ResourceID)
	}
	if record.Result["instance_id"] != instanceID {
		t.Fatalf("result.instance_id = %v, want %s", record.Result["instance_id"], instanceID)
	}
	if record.Result["kind"] != "gpu_container" || record.Result["state"] != "running" {
		t.Fatalf("result snapshot = %v, want kind/state at write time", record.Result)
	}
	if record.CreatedAt.IsZero() {
		t.Fatal("created_at is zero, want write-time stamp")
	}
}

func TestInstanceLifecycleActionsWriteTasks(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	instanceID := harness.createGPUContainer(t, "task-lifecycle-write")

	for _, action := range []string{"stop", "start", "restart", "delete"} {
		key := "task-lifecycle-" + action
		harness.lifecycle(t, instanceID, action, key)
		record := harness.singleTaskByType(t, "instance."+action)
		if record.Status != "running" || record.ProgressPct != 10 {
			t.Fatalf("instance.%s task = (%s, %d), want (running, 10)", action, record.Status, record.ProgressPct)
		}
		if record.ResourceType != "instance" || record.Result["instance_id"] != instanceID {
			t.Fatalf("instance.%s task = %+v, want instance-bound record", action, record)
		}
		if record.Result["action"] != action {
			t.Fatalf("result.action = %v, want %s", record.Result["action"], action)
		}
	}
	// A rejected lifecycle (deleted instance cannot stop) answers 409 and
	// must not add a task row: the write point runs after error checks.
	rejectBody := `{"action":"stop","idempotency_key":"task-lifecycle-extra-stop"}`
	performJSONRequest(t, harness.h, http.MethodPost, "/api/v1/instances/"+instanceID+"/lifecycle", rejectBody, http.StatusConflict)
	records, _, _ := harness.tasks.List(context.Background(), harness.tenantID, ports.AsyncTaskListFilter{Limit: 100})
	if len(records) != 5 { // create + stop + start + restart + delete
		t.Fatalf("task records = %d, want 5", len(records))
	}
}

func TestInstanceCreateIdempotentReplayWritesSingleTask(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	body := `{"kind":"gpu_container","name":"gpu-replay","idempotency_key":"task-replay-key","cpu":"2","memory":"4Gi"}`
	performJSONRequest(t, harness.h, http.MethodPost, "/api/v1/instances", body, http.StatusCreated)
	// Completed replay answers 409 and the write point re-runs harmlessly:
	// ON CONFLICT keeps the store at exactly one row.
	performJSONRequest(t, harness.h, http.MethodPost, "/api/v1/instances", body, http.StatusConflict)
	harness.singleTaskByType(t, "instance.create")

	records, _, err := harness.tasks.List(context.Background(), harness.tenantID, ports.AsyncTaskListFilter{TaskType: "instance.create", Limit: 10})
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("instance.create records = %d, want 1 (no duplicate on replay)", len(records))
	}
}

// failingCreateStore simulates a task store outage: Create fails while fail
// is set; every other operation delegates.
type failingCreateStore struct {
	ports.AsyncTaskStore
	fail atomic.Bool
}

func (s *failingCreateStore) Create(ctx context.Context, record ports.AsyncTaskRecord) (ports.AsyncTaskRecord, bool, error) {
	if s.fail.Load() {
		return ports.AsyncTaskRecord{}, false, errors.New("task store unavailable")
	}
	return s.AsyncTaskStore.Create(ctx, record)
}

func TestInstanceCreateReplayRepairsMissingTaskRecord(t *testing.T) {
	real := runtimeadapter.NewLocalAsyncTaskStore()
	flaky := &failingCreateStore{AsyncTaskStore: real}
	flaky.fail.Store(true)
	// Phase 1: the task write fails, the instance create still succeeds.
	harness := newTaskTestHarness(t, flaky)
	instanceID := harness.createGPUContainer(t, "task-repair-key")
	if records, _, _ := real.List(context.Background(), harness.tenantID, ports.AsyncTaskListFilter{TaskType: "instance.create", Limit: 10}); len(records) != 0 {
		t.Fatalf("records = %d, want 0 while store create fails", len(records))
	}

	// Phase 2: store recovered; the client retries the same idempotency key
	// and the replay path repairs the missing audit record. The body must
	// match the Phase 1 fingerprint (same name) or the replay is rejected
	// as a different intent before reaching the write point.
	flaky.fail.Store(false)
	body := `{"kind":"gpu_container","name":"gpu-task-repair-key","idempotency_key":"task-repair-key","cpu":"2","memory":"4Gi"}`
	performJSONRequest(t, harness.h, http.MethodPost, "/api/v1/instances", body, http.StatusConflict)
	record := harness.singleTaskByType(t, "instance.create")
	if record.Status != "running" || record.Result["instance_id"] != instanceID {
		t.Fatalf("repaired task = %+v, want running record bound to the real instance", record)
	}
	// A further normal retry produces no duplicate row.
	performJSONRequest(t, harness.h, http.MethodPost, "/api/v1/instances", body, http.StatusConflict)
	records, _, _ := real.List(context.Background(), harness.tenantID, ports.AsyncTaskListFilter{TaskType: "instance.create", Limit: 10})
	if len(records) != 1 {
		t.Fatalf("instance.create records = %d, want 1 after repair", len(records))
	}
}

func TestInstanceTaskStoreUnavailableKeepsInstanceResponse(t *testing.T) {
	flaky := &failingCreateStore{AsyncTaskStore: runtimeadapter.NewLocalAsyncTaskStore()}
	flaky.fail.Store(true)
	harness := newTaskTestHarness(t, flaky)
	// The instance main response is unaffected by the task store outage.
	harness.createGPUContainer(t, "task-outage-key")
	harness.lifecycle(t, mustFirstInstanceID(t, harness), "stop", "task-outage-stop")
}

func mustFirstInstanceID(t *testing.T, harness *taskTestHarness) string {
	t.Helper()
	records, err := harness.injected.service.List(context.Background(), ports.WorkloadInstanceListRequest{TenantID: harness.tenantID})
	if err != nil || len(records) == 0 {
		t.Fatalf("instance List = (%d, %v), want at least one instance", len(records), err)
	}
	return records[0].InstanceID
}

// --- §8.2 lazy sync over HTTP ---

func TestTaskGetLazySyncCreateFlow(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	instanceID := harness.createGPUContainer(t, "task-lazy-create")
	task := harness.singleTaskByType(t, "instance.create")

	// provisioning -> running/20
	harness.setInstanceState(t, instanceID, ports.WorkloadStateProvisioning)
	fetched := harness.getTask(t, task.ID)
	if fetched["status"] != "running" || fetched["progress_pct"].(float64) != 20 {
		t.Fatalf("lazy sync at provisioning = (%v, %v), want (running, 20)", fetched["status"], fetched["progress_pct"])
	}

	// running -> completed/100 with completed_at
	harness.setInstanceState(t, instanceID, ports.WorkloadStateRunning)
	fetched = harness.getTask(t, task.ID)
	if fetched["status"] != "completed" || fetched["progress_pct"].(float64) != 100 {
		t.Fatalf("lazy sync at running = (%v, %v), want (completed, 100)", fetched["status"], fetched["progress_pct"])
	}
	if completedAt, _ := fetched["completed_at"].(string); completedAt == "" {
		t.Fatal("completed_at empty, want non-zero timestamp")
	}

	// Terminal row: further GETs keep returning the terminal snapshot.
	fetched = harness.getTask(t, task.ID)
	if fetched["status"] != "completed" {
		t.Fatalf("terminal GET = %v, want completed", fetched["status"])
	}
}

func TestTaskGetLazySyncStuckProvisioningStaysRunning(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	instanceID := harness.createGPUContainer(t, "task-lazy-stuck")
	task := harness.singleTaskByType(t, "instance.create")

	harness.setInstanceState(t, instanceID, ports.WorkloadStateProvisioning)
	for i := 0; i < 3; i++ {
		fetched := harness.getTask(t, task.ID)
		if fetched["status"] != "running" || fetched["progress_pct"].(float64) != 20 {
			t.Fatalf("stuck provisioning GET #%d = (%v, %v), want honest (running, 20)", i, fetched["status"], fetched["progress_pct"])
		}
	}
}

func TestTaskGetLazySyncStopAndDeleteFlows(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	instanceID := harness.createGPUContainer(t, "task-lazy-stop-delete")

	harness.lifecycle(t, instanceID, "stop", "task-lazy-stop")
	stopTask := harness.singleTaskByType(t, "instance.stop")
	harness.setInstanceState(t, instanceID, ports.WorkloadStateStopping)
	fetched := harness.getTask(t, stopTask.ID)
	if fetched["status"] != "running" || fetched["progress_pct"].(float64) != 60 {
		t.Fatalf("stop at stopping = (%v, %v), want (running, 60)", fetched["status"], fetched["progress_pct"])
	}
	harness.setInstanceState(t, instanceID, ports.WorkloadStateStopped)
	fetched = harness.getTask(t, stopTask.ID)
	if fetched["status"] != "completed" || fetched["progress_pct"].(float64) != 100 {
		t.Fatalf("stop at stopped = (%v, %v), want (completed, 100)", fetched["status"], fetched["progress_pct"])
	}

	harness.lifecycle(t, instanceID, "delete", "task-lazy-delete")
	deleteTask := harness.singleTaskByType(t, "instance.delete")
	// Instance still present in deleting state: task keeps running at 80.
	harness.setInstanceState(t, instanceID, ports.WorkloadStateDeleting)
	fetched = harness.getTask(t, deleteTask.ID)
	if fetched["status"] != "running" || fetched["progress_pct"].(float64) != 80 {
		t.Fatalf("delete at deleting = (%v, %v), want (running, 80)", fetched["status"], fetched["progress_pct"])
	}
	// Main terminal: state=deleted (records are never removed from the store).
	harness.setInstanceState(t, instanceID, ports.WorkloadStateDeleted)
	fetched = harness.getTask(t, deleteTask.ID)
	if fetched["status"] != "completed" || fetched["progress_pct"].(float64) != 100 {
		t.Fatalf("delete at deleted = (%v, %v), want (completed, 100)", fetched["status"], fetched["progress_pct"])
	}
}

func TestTaskGetLazySyncFailedAndGoneMappings(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	instanceID := harness.createGPUContainer(t, "task-lazy-failed")

	for _, action := range []string{"start", "stop", "restart"} {
		key := "task-lazy-failed-" + action
		// Reset to running each iteration: stop leaves the instance stopped
		// and restart requires running, so a shared instance must be reset
		// between actions.
		harness.setInstanceState(t, instanceID, ports.WorkloadStateRunning)
		harness.lifecycle(t, instanceID, action, key)
		task := harness.singleTaskByType(t, "instance."+action)
		harness.setInstanceState(t, instanceID, ports.WorkloadStateFailed)
		fetched := harness.getTask(t, task.ID)
		if fetched["status"] != "failed" {
			t.Fatalf("instance.%s at failed = %v, want failed", action, fetched["status"])
		}
		if message, _ := fetched["error_message"].(string); message == "" {
			t.Fatalf("instance.%s failed task error_message empty, want non-empty", action)
		}
	}

	// Non-delete action on a vanished record: failed (defensive mapping).
	goneTask := seedInstanceTask(t, harness, "instance.create", "inst_99999999-9999-4999-8999-999999999999", "task-gone-create")
	fetched := harness.getTask(t, goneTask.ID)
	if fetched["status"] != "failed" {
		t.Fatalf("create on gone record = %v, want failed", fetched["status"])
	}
	// delete on a vanished record: completed (defensive branch).
	goneDelete := seedInstanceTask(t, harness, "instance.delete", "inst_99999999-9999-4999-8999-999999999999", "task-gone-delete")
	fetched = harness.getTask(t, goneDelete.ID)
	if fetched["status"] != "completed" || fetched["progress_pct"].(float64) != 100 {
		t.Fatalf("delete on gone record = (%v, %v), want (completed, 100)", fetched["status"], fetched["progress_pct"])
	}
}

func TestTaskGetLazySyncRestartIntermediateStatesNeverTerminal(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	instanceID := harness.createGPUContainer(t, "task-lazy-restart")
	harness.lifecycle(t, instanceID, "restart", "task-lazy-restart-key")
	task := harness.singleTaskByType(t, "instance.restart")

	for _, state := range []ports.WorkloadState{
		ports.WorkloadStateStopped, ports.WorkloadStateStarting, ports.WorkloadStateStopping,
	} {
		harness.setInstanceState(t, instanceID, state)
		fetched := harness.getTask(t, task.ID)
		if fetched["status"] != "running" || fetched["progress_pct"].(float64) != 40 {
			t.Fatalf("restart at %s = (%v, %v), want (running, 40)", state, fetched["status"], fetched["progress_pct"])
		}
	}
	harness.setInstanceState(t, instanceID, ports.WorkloadStateRunning)
	fetched := harness.getTask(t, task.ID)
	if fetched["status"] != "completed" || fetched["progress_pct"].(float64) != 100 {
		t.Fatalf("restart at running = (%v, %v), want (completed, 100)", fetched["status"], fetched["progress_pct"])
	}
}

func TestTaskListReturnsStoreSnapshotWithoutLazySync(t *testing.T) {
	harness := newTaskTestHarness(t, runtimeadapter.NewLocalAsyncTaskStore())
	instanceID := harness.createGPUContainer(t, "task-list-snapshot")
	task := harness.singleTaskByType(t, "instance.create")

	// Instance has converged to running but the task was never single-queried:
	// list keeps returning the write-time snapshot (running/10).
	harness.setInstanceState(t, instanceID, ports.WorkloadStateRunning)
	listed := harness.listTasks(t, "?task_type=instance.create")
	items, _ := listed["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items = %d, want 1", len(items))
	}
	item, _ := items[0].(map[string]any)
	if item["id"] != task.ID || item["status"] != "running" || item["progress_pct"].(float64) != 10 {
		t.Fatalf("list snapshot = (%v, %v, %v), want stored (running, 10) — list must not lazy-sync", item["id"], item["status"], item["progress_pct"])
	}
}

// seedInstanceTask inserts an instance task bound to instanceID without going
// through the HTTP write point (used for defensive mapping branches).
func seedInstanceTask(t *testing.T, harness *taskTestHarness, taskType, instanceID, key string) ports.AsyncTaskRecord {
	t.Helper()
	created, _, err := harness.tasks.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID:       harness.tenantID,
		IdempotencyKey: key,
		TaskType:       taskType,
		ResourceType:   "instance",
		ResourceID:     strings.TrimPrefix(instanceID, "inst_"),
		Status:         "running",
		ProgressPct:    10,
		Result:         map[string]any{"instance_id": instanceID},
		MaxAttempts:    1,
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed task %s error = %v", taskType, err)
	}
	return created
}

// --- §8.2 lazy sync built-in refresh (does not depend on instance list) ---

func newFakeK8sClient(t *testing.T, deployments map[string]string) (*runtimeadapter.KubernetesRESTClient, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	for name, phase := range deployments {
		ready := 0
		replicas := 1
		switch phase {
		case "Running":
			ready = 1
		case "Pending":
			replicas = 0
		}
		response := fmt.Sprintf(`{"metadata":{"name":%q,"creationTimestamp":"2026-01-01T00:00:00Z"},"spec":{"template":{"spec":{"containers":[{"resources":{"limits":{}}}]}}},"status":{"replicas":%d,"readyReplicas":%d,"availableReplicas":%d}}`, name, replicas, ready, ready)
		mux.HandleFunc("/apis/apps/v1/namespaces/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, response)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"items":[]}`)
	})
	client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
		Host:       srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("fake K8s client error = %v", err)
	}
	return client, srv
}

func TestTaskGetLazySyncRefreshesInstanceWithoutList(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	k8sClient, srv := newFakeK8sClient(t, map[string]string{"gpu-refresh": "Running"})
	defer srv.Close()
	harness := newTaskTestHarnessWithK8s(t, tasks, k8sClient)
	instanceID := harness.createGPUContainer(t, "task-refresh-key")
	task := harness.singleTaskByType(t, "instance.create")

	// Store snapshot is stale at provisioning while the Deployment is Ready.
	record, err := harness.injected.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   harness.tenantID,
		InstanceID: instanceID,
	})
	if err != nil {
		t.Fatalf("service.Get error = %v", err)
	}
	record.Status.State = ports.WorkloadStateProvisioning
	if err := harness.injected.store.UpsertStatus(context.Background(), record); err != nil {
		t.Fatalf("UpsertStatus error = %v", err)
	}

	// The instance list endpoint is never called: the task GET alone must
	// refresh the instance and advance to the terminal state.
	fetched := harness.getTask(t, task.ID)
	if fetched["status"] != "completed" || fetched["progress_pct"].(float64) != 100 {
		t.Fatalf("lazy sync with built-in refresh = (%v, %v), want (completed, 100) — a pure service.Get would stay running/20", fetched["status"], fetched["progress_pct"])
	}
	refreshed, err := harness.injected.service.Get(context.Background(), ports.WorkloadInstanceGetRequest{
		TenantID:   harness.tenantID,
		InstanceID: instanceID,
	})
	if err != nil {
		t.Fatalf("service.Get after sync error = %v", err)
	}
	if refreshed.Status.State != ports.WorkloadStateRunning {
		t.Fatalf("instance state after refresh = %s, want running written back", refreshed.Status.State)
	}
}

// --- §8.2 lazy sync degradation and write amplification ---

func TestTaskGetLazySyncDegradesGracefully(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	harness := newTaskTestHarness(t, tasks)
	instanceID := harness.createGPUContainer(t, "task-degrade-key")
	task := harness.singleTaskByType(t, "instance.create")

	// Observer failure: the GET still answers 200 with the stored snapshot.
	degraded := server.New()
	degraded.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	failingObserver := func(context.Context, string, string) (ports.WorkloadInstanceRecord, error) {
		return ports.WorkloadInstanceRecord{}, errors.New("instance service unavailable")
	}
	registerTasksWithStore(degraded.Group("/api/v1"), tasks, failingObserver)
	body := performJSONRequest(t, degraded, http.MethodGet, "/api/v1/tasks/"+task.ID, "", http.StatusOK)
	fetched := decodedObject(t, body, "")
	if fetched["status"] != "running" || fetched["progress_pct"].(float64) != 10 {
		t.Fatalf("observer failure GET = (%v, %v), want stored (running, 10)", fetched["status"], fetched["progress_pct"])
	}

	// Update failure: same degradation to the stored snapshot.
	harness.setInstanceState(t, instanceID, ports.WorkloadStateRunning)
	failingUpdate := &updateFailingStore{AsyncTaskStore: tasks}
	updateServer := server.New()
	updateServer.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	observerServer := newTaskTestHarness(t, tasks) // reuse a healthy observer
	registerTasksWithStore(updateServer.Group("/api/v1"), failingUpdate, func(ctx context.Context, tenantID, instID string) (ports.WorkloadInstanceRecord, error) {
		record, err := observerServer.injected.service.Get(ctx, ports.WorkloadInstanceGetRequest{TenantID: tenantID, InstanceID: instID})
		if err != nil {
			return ports.WorkloadInstanceRecord{}, err
		}
		observerServer.injected.refreshOneStoreStatus(ctx, &record)
		return record, nil
	})
	body = performJSONRequest(t, updateServer, http.MethodGet, "/api/v1/tasks/"+task.ID, "", http.StatusOK)
	fetched = decodedObject(t, body, "")
	if fetched["status"] != "running" {
		t.Fatalf("update failure GET = %v, want stored running snapshot", fetched["status"])
	}
}

// updateFailingStore delegates everything except Update, which always fails.
type updateFailingStore struct {
	ports.AsyncTaskStore
}

func (s *updateFailingStore) Update(context.Context, ports.AsyncTaskUpdate) (ports.AsyncTaskRecord, error) {
	return ports.AsyncTaskRecord{}, errors.New("update failed")
}

// countingAsyncTaskStore counts Update calls to assert write amplification.
type countingAsyncTaskStore struct {
	ports.AsyncTaskStore
	updates atomic.Int64
}

func (s *countingAsyncTaskStore) Update(ctx context.Context, update ports.AsyncTaskUpdate) (ports.AsyncTaskRecord, error) {
	s.updates.Add(1)
	return s.AsyncTaskStore.Update(ctx, update)
}

func TestTaskGetLazySyncSuppressesWriteAmplification(t *testing.T) {
	counting := &countingAsyncTaskStore{AsyncTaskStore: runtimeadapter.NewLocalAsyncTaskStore()}
	harness := newTaskTestHarness(t, counting)
	instanceID := harness.createGPUContainer(t, "task-amplification-key")
	task := harness.singleTaskByType(t, "instance.create")

	// First GET observes provisioning and must write once (10 -> 20 jump).
	harness.setInstanceState(t, instanceID, ports.WorkloadStateProvisioning)
	harness.getTask(t, task.ID)
	if counting.updates.Load() != 1 {
		t.Fatalf("updates after first GET = %d, want 1", counting.updates.Load())
	}
	// Stable state: repeated GETs must not write again.
	harness.getTask(t, task.ID)
	harness.getTask(t, task.ID)
	if counting.updates.Load() != 1 {
		t.Fatalf("updates after repeated GETs = %d, want 1 (write amplification suppressed)", counting.updates.Load())
	}
}

// --- §8.2 mapping table unit coverage (every row of §5.2) ---

func TestInstanceTaskAdvanceMappingTable(t *testing.T) {
	current := func() ports.AsyncTaskRecord {
		return ports.AsyncTaskRecord{
			Status: "running", ProgressPct: 10,
			Result: map[string]any{"instance_id": "inst_1"},
		}
	}
	tests := []struct {
		name          string
		action        string
		observeErr    error
		state         ports.WorkloadState
		wantStatus    string
		wantProgress  int
		wantTerminal  bool
		wantCompleted bool
		wantError     string
	}{
		// create / start / restart rows
		{name: "create running", action: "create", state: ports.WorkloadStateRunning, wantStatus: "completed", wantProgress: 100, wantTerminal: true, wantCompleted: true},
		{name: "create failed", action: "create", state: ports.WorkloadStateFailed, wantStatus: "failed", wantTerminal: true, wantError: "instance entered failed state"},
		{name: "create provisioning", action: "create", state: ports.WorkloadStateProvisioning, wantStatus: "running", wantProgress: 20},
		{name: "create pending", action: "create", state: ports.WorkloadStatePending, wantStatus: "running", wantProgress: 20},
		{name: "create starting", action: "create", state: ports.WorkloadStateStarting, wantStatus: "running", wantProgress: 40},
		{name: "create stopping", action: "create", state: ports.WorkloadStateStopping, wantStatus: "running", wantProgress: 40},
		{name: "create stopped", action: "create", state: ports.WorkloadStateStopped, wantStatus: "running", wantProgress: 40},
		{name: "create deleting", action: "create", state: ports.WorkloadStateDeleting, wantStatus: "running", wantProgress: 40},
		{name: "create deleted", action: "create", state: ports.WorkloadStateDeleted, wantStatus: "failed", wantTerminal: true, wantError: "instance deleted before reaching running"},
		{name: "create gone", action: "create", observeErr: ports.ErrNotFound, wantStatus: "failed", wantTerminal: true, wantError: "instance record not found"},
		{name: "start running", action: "start", state: ports.WorkloadStateRunning, wantStatus: "completed", wantProgress: 100, wantTerminal: true, wantCompleted: true},
		{name: "restart running", action: "restart", state: ports.WorkloadStateRunning, wantStatus: "completed", wantProgress: 100, wantTerminal: true, wantCompleted: true},
		// stop rows
		{name: "stop stopped", action: "stop", state: ports.WorkloadStateStopped, wantStatus: "completed", wantProgress: 100, wantTerminal: true, wantCompleted: true},
		{name: "stop failed", action: "stop", state: ports.WorkloadStateFailed, wantStatus: "failed", wantTerminal: true, wantError: "instance entered failed state"},
		{name: "stop stopping", action: "stop", state: ports.WorkloadStateStopping, wantStatus: "running", wantProgress: 60},
		{name: "stop deleting", action: "stop", state: ports.WorkloadStateDeleting, wantStatus: "running", wantProgress: 60},
		{name: "stop provisioning", action: "stop", state: ports.WorkloadStateProvisioning, wantStatus: "running", wantProgress: 30},
		{name: "stop pending", action: "stop", state: ports.WorkloadStatePending, wantStatus: "running", wantProgress: 30},
		{name: "stop starting", action: "stop", state: ports.WorkloadStateStarting, wantStatus: "running", wantProgress: 30},
		{name: "stop running", action: "stop", state: ports.WorkloadStateRunning, wantStatus: "running", wantProgress: 30},
		{name: "stop deleted", action: "stop", state: ports.WorkloadStateDeleted, wantStatus: "failed", wantTerminal: true, wantError: "instance deleted before reaching stopped"},
		{name: "stop gone", action: "stop", observeErr: ports.ErrNotFound, wantStatus: "failed", wantTerminal: true, wantError: "instance record not found"},
		// delete rows
		{name: "delete deleted main terminal", action: "delete", state: ports.WorkloadStateDeleted, wantStatus: "completed", wantProgress: 100, wantTerminal: true, wantCompleted: true},
		{name: "delete gone defensive", action: "delete", observeErr: ports.ErrNotFound, wantStatus: "completed", wantProgress: 100, wantTerminal: true, wantCompleted: true},
		{name: "delete deleting", action: "delete", state: ports.WorkloadStateDeleting, wantStatus: "running", wantProgress: 80},
		{name: "delete running", action: "delete", state: ports.WorkloadStateRunning, wantStatus: "running", wantProgress: 80},
		{name: "delete stopped", action: "delete", state: ports.WorkloadStateStopped, wantStatus: "running", wantProgress: 80},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := current()
			if test.observeErr == nil {
				record.Result["state"] = string(test.state)
			}
			inst := ports.WorkloadInstanceRecord{}
			if test.observeErr == nil {
				inst.Status.State = test.state
			}
			update, terminal, err := instanceTaskAdvance(test.action, test.observeErr, inst, record)
			if err != nil {
				t.Fatalf("instanceTaskAdvance error = %v", err)
			}
			if update.Status != test.wantStatus {
				t.Fatalf("status = %q, want %q", update.Status, test.wantStatus)
			}
			if test.wantProgress > 0 && update.ProgressPct != test.wantProgress {
				t.Fatalf("progress = %d, want %d", update.ProgressPct, test.wantProgress)
			}
			if terminal != test.wantTerminal {
				t.Fatalf("terminal = %v, want %v", terminal, test.wantTerminal)
			}
			if test.wantCompleted && update.CompletedAt.IsZero() {
				t.Fatal("completed_at zero on completed mapping, want timestamp")
			}
			if !test.wantCompleted && !update.CompletedAt.IsZero() {
				t.Fatalf("completed_at set on %q mapping, want zero", update.Status)
			}
			if test.wantError != "" && update.ErrorMessage != test.wantError {
				t.Fatalf("error_message = %q, want %q", update.ErrorMessage, test.wantError)
			}
		})
	}

	// Transient observation errors propagate for caller-side degradation.
	if _, _, err := instanceTaskAdvance("create", errors.New("boom"), ports.WorkloadInstanceRecord{}, current()); err == nil {
		t.Fatal("transient observe error = nil, want propagated error")
	}
}

// --- §8.2 concurrent out-of-order writes never revert a terminal row ---

func TestTaskGetLazySyncConcurrentOutOfOrderWrites(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	harness := newTaskTestHarness(t, tasks)
	instanceID := harness.createGPUContainer(t, "task-race-key")
	task := harness.singleTaskByType(t, "instance.create")

	// GET A observes running and lands the terminal write first.
	harness.setInstanceState(t, instanceID, ports.WorkloadStateRunning)
	harness.getTask(t, task.ID)
	stored, err := tasks.Get(context.Background(), harness.tenantID, task.ID)
	if err != nil || stored.Status != "completed" {
		t.Fatalf("stored task after terminal write = (%s, %v), want completed", stored.Status, err)
	}

	// A late stale writer (observed provisioning before the terminal write)
	// must not revert the terminal row; the guard returns the current record.
	stale, err := tasks.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: harness.tenantID, ID: task.ID, Status: "running", ProgressPct: 20,
		Result: map[string]any{"state": "provisioning"},
	})
	if err != nil {
		t.Fatalf("stale Update error = %v, want guard hit without error", err)
	}
	if stale.Status != "completed" || stale.ProgressPct != 100 || stale.CompletedAt.IsZero() {
		t.Fatalf("stale Update = (%s, %d), want terminal row to win", stale.Status, stale.ProgressPct)
	}
}

// --- §8.2 orphan retry path also writes the task record ---

func TestInstanceLifecycleOrphanRetryWritesTask(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	// One deployment alive in the fake cluster; the in-memory store starts
	// empty, mimicking a gateway restart.
	k8sClient, srv := newFakeK8sOrphanServer(t, "orphan-dep-01")
	defer srv.Close()
	harness := newTaskTestHarnessWithK8s(t, tasks, k8sClient)

	body := `{"action":"stop","idempotency_key":"task-orphan-stop"}`
	performJSONRequest(t, harness.h, http.MethodPost, "/api/v1/instances/orphan-dep-01/lifecycle", body, http.StatusOK)

	record := harness.singleTaskByType(t, "instance.stop")
	if record.Status != "running" || record.Result["instance_id"] != "orphan-dep-01" {
		t.Fatalf("orphan retry task = (%s, %v), want running record bound to the imported instance", record.Status, record.Result["instance_id"])
	}
}

func newFakeK8sOrphanServer(t *testing.T, deploymentName string) (*runtimeadapter.KubernetesRESTClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/deployments"):
			_, _ = fmt.Fprintf(w, `{"items":[{"metadata":{"name":%q,"creationTimestamp":"2026-01-01T00:00:00Z"}}]}`, deploymentName)
		case strings.Contains(r.URL.Path, "/deployments/"+deploymentName):
			_, _ = fmt.Fprintf(w, `{"metadata":{"name":%q,"creationTimestamp":"2026-01-01T00:00:00Z"},"spec":{"template":{"spec":{"containers":[{"resources":{"limits":{}}}]}}},"status":{"replicas":1,"readyReplicas":1,"availableReplicas":1}}`, deploymentName)
		default:
			_, _ = fmt.Fprint(w, `{"items":[]}`)
		}
	}))
	client, err := runtimeadapter.NewKubernetesRESTClient(runtimeadapter.KubernetesRESTClientConfig{
		Host:       srv.URL,
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("fake K8s client error = %v", err)
	}
	return client, srv
}

// --- §8.2 response fields, mode B visibility, kb domain ---

func TestTaskResponseFieldsComplete(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	harness := newTaskTestHarness(t, tasks)
	instanceID := harness.createGPUContainer(t, "task-fields-key")
	task := harness.singleTaskByType(t, "instance.create")

	// A failed task carries a non-empty error_message; the UUID resource_id
	// is returned; dead_letter_at appears when set.
	_, err := tasks.Update(context.Background(), ports.AsyncTaskUpdate{
		TenantID: harness.tenantID, ID: task.ID, Status: "failed", ProgressPct: 10,
		Result: map[string]any{"instance_id": instanceID}, ErrorMessage: "instance entered failed state",
		DeadLetterAt: time.Unix(7000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Update failed task error = %v", err)
	}
	fetched := harness.getTask(t, task.ID)
	if fetched["error_message"] != "instance entered failed state" {
		t.Fatalf("error_message = %v, want failure reason", fetched["error_message"])
	}
	if got, _ := fetched["resource_id"].(string); got == "" || strings.HasPrefix(got, "inst_") {
		t.Fatalf("resource_id = %v, want bare UUID", fetched["resource_id"])
	}
	if got, _ := fetched["dead_letter_at"].(string); got == "" {
		t.Fatalf("dead_letter_at = %v, want timestamp", fetched["dead_letter_at"])
	}

	listed := harness.listTasks(t, "?status=failed")
	items, _ := listed["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("failed list items = %d, want 1", len(items))
	}
	item, _ := items[0].(map[string]any)
	for _, field := range []string{"resource_id", "error_message", "dead_letter_at", "idempotency_key", "attempt_count", "max_attempts"} {
		if _, present := item[field]; !present {
			t.Fatalf("list item missing contract field %q: %v", field, item)
		}
	}
}

func TestTaskListReturnsModeBAndKbDomainTasksUnchanged(t *testing.T) {
	tasks := runtimeadapter.NewLocalAsyncTaskStore()
	h := server.New()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		c.Set("tenant_id", "tenant-a")
		c.Next(ctx)
	})
	v1 := h.Group("/api/v1")
	registerStorageResourcesWithServiceAndTasks(v1, runtimeadapter.NewLocalStorageService(), tasks)
	registerTasksWithStore(v1, tasks, nil)

	// A mode B storage task (202 accepted-then-completed semantics).
	created := performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes", `{"idempotency_key":"mixed-mode-b","name":"mixed-data","size_gib":8}`, http.StatusCreated)
	volumeID := jsonStringField(t, created, "id")
	expandBody := `{"idempotency_key":"mixed-expand","size_gib":16}`
	accepted := performJSONRequest(t, h, http.MethodPost, "/api/v1/volumes/"+volumeID+"/expand", expandBody, http.StatusAccepted)
	taskID := jsonStringField(t, accepted, "id")

	// A kb-service-shaped pending task and a completed CreateKB task.
	seedKbTask(t, tasks, "kb.parse", "pending", "kb-mixed-parse")
	seedKbTask(t, tasks, "kb.create", "completed", "kb-mixed-create")

	// Mode B tasks surface in list with the new fields intact.
	mixed := decodedObject(t, performJSONRequest(t, h, http.MethodGet, "/api/v1/tasks?resource_type=volume", "", http.StatusOK), "")
	items, _ := mixed["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("volume list items = %d, want 1", len(items))
	}
	item, _ := items[0].(map[string]any)
	if item["task_type"] != "volume.expand" || item["resource_type"] != "volume" {
		t.Fatalf("mode B list item = %v, want volume.expand task", item)
	}

	// kb domain: pending tasks stay pending in list and single GET — the
	// Gateway never advances kb tasks.
	kbList := decodedObject(t, performJSONRequest(t, h, http.MethodGet, "/api/v1/tasks?task_type=kb.parse", "", http.StatusOK), "")
	items, _ = kbList["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("kb.parse list items = %d, want 1", len(items))
	}
	kbItem, _ := items[0].(map[string]any)
	if kbItem["status"] != "pending" {
		t.Fatalf("kb.parse status = %v, want pending (kb noise is expected)", kbItem["status"])
	}
	kbRecord, _, _ := tasks.List(context.Background(), "tenant-a", ports.AsyncTaskListFilter{TaskType: "kb.parse", Limit: 10})
	kbGet := decodedObject(t, performJSONRequest(t, h, http.MethodGet, "/api/v1/tasks/"+kbRecord[0].ID, "", http.StatusOK), "")
	if kbGet["status"] != "pending" {
		t.Fatalf("kb.parse single GET = %v, want pending (no lazy sync for kb)", kbGet["status"])
	}

	// The completed CreateKB task is returned as stored.
	kbCreateList := decodedObject(t, performJSONRequest(t, h, http.MethodGet, "/api/v1/tasks?task_type=kb.create", "", http.StatusOK), "")
	items, _ = kbCreateList["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("kb.create list items = %d, want 1", len(items))
	}
	kbCreateItem, _ := items[0].(map[string]any)
	if kbCreateItem["status"] != "completed" {
		t.Fatalf("kb.create status = %v, want completed", kbCreateItem["status"])
	}

	// The storage task itself is unaffected by the lazy-sync machinery.
	storageGet := decodedObject(t, performJSONRequest(t, h, http.MethodGet, "/api/v1/tasks/"+taskID, "", http.StatusOK), "")
	if storageGet["task_type"] != "volume.expand" || storageGet["status"] != "completed" {
		t.Fatalf("storage task = %v, want completed volume.expand", storageGet)
	}
}

func seedKbTask(t *testing.T, tasks ports.AsyncTaskStore, taskType, status, key string) {
	t.Helper()
	if _, _, err := tasks.Create(context.Background(), ports.AsyncTaskRecord{
		TenantID:       "tenant-a",
		IdempotencyKey: key,
		TaskType:       taskType,
		ResourceType:   "kb_document",
		Status:         status,
		ProgressPct:    0,
		Result:         map[string]any{},
		MaxAttempts:    1,
		CreatedAt:      time.Unix(6000, 0).UTC(),
	}); err != nil {
		t.Fatalf("seed kb task %s error = %v", taskType, err)
	}
}
