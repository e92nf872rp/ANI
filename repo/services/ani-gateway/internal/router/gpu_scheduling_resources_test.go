package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/kubercloud/ani/pkg/ports"
	"github.com/kubercloud/ani/services/ani-gateway/internal/middleware"
)

// fakeQueueStore is an in-memory GPUSchedulingQueueStore for handler tests.
type fakeQueueStore struct {
	queues             map[string]ports.GPUSchedulingQueue
	byName             map[string]string
	byCreateKey        map[string]string // idempotencyKey -> queueID
	byUpdateKey        map[string]string // idempotencyKey -> queueID
	platformDefaultIDs map[string]bool
	failNext           bool
}

func newFakeQueueStore() *fakeQueueStore {
	return &fakeQueueStore{
		queues:             map[string]ports.GPUSchedulingQueue{},
		byName:             map[string]string{},
		byCreateKey:        map[string]string{},
		byUpdateKey:        map[string]string{},
		platformDefaultIDs: map[string]bool{},
	}
}

func (f *fakeQueueStore) List(ctx context.Context, tenantID string) ([]ports.GPUSchedulingQueue, error) {
	if f.failNext {
		f.failNext = false
		return nil, ports.ErrQueueStoreUnavailable
	}
	result := make([]ports.GPUSchedulingQueue, 0, len(f.queues))
	for _, q := range f.queues {
		result = append(result, q)
	}
	return result, nil
}

func (f *fakeQueueStore) Get(ctx context.Context, tenantID, id string) (ports.GPUSchedulingQueue, error) {
	if f.failNext {
		f.failNext = false
		return ports.GPUSchedulingQueue{}, ports.ErrQueueStoreUnavailable
	}
	q, ok := f.queues[id]
	if !ok {
		return ports.GPUSchedulingQueue{}, ports.ErrQueueNotFound
	}
	return q, nil
}

func (f *fakeQueueStore) Create(ctx context.Context, tenantID, idempotencyKey string, req ports.GPUSchedulingQueueCreateRequest) (ports.GPUSchedulingQueueCreateResult, error) {
	if f.failNext {
		f.failNext = false
		return ports.GPUSchedulingQueueCreateResult{}, ports.ErrQueueStoreUnavailable
	}
	// Idempotency replay
	if idempotencyKey != "" {
		if existingID, ok := f.byCreateKey[idempotencyKey]; ok {
			return ports.GPUSchedulingQueueCreateResult{Queue: f.queues[existingID], IdempotentReplay: true}, nil
		}
	}
	key := tenantID + "|" + req.Name
	if _, exists := f.byName[key]; exists {
		return ports.GPUSchedulingQueueCreateResult{}, ports.ErrQueueNameConflict
	}
	now := time.Now().UTC()
	q := ports.GPUSchedulingQueue{
		ID:            "queue-" + req.Name,
		Name:          req.Name,
		Weight:        req.Weight,
		Reclaimable:   req.Reclaimable,
		WorkloadClass: req.WorkloadClass,
		ProjectID:     req.ProjectID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	f.queues[q.ID] = q
	f.byName[key] = q.ID
	if idempotencyKey != "" {
		f.byCreateKey[idempotencyKey] = q.ID
	}
	return ports.GPUSchedulingQueueCreateResult{Queue: q}, nil
}

func (f *fakeQueueStore) Update(ctx context.Context, tenantID, id, idempotencyKey string, req ports.GPUSchedulingQueueUpdateRequest) (ports.GPUSchedulingQueueUpdateResult, error) {
	if f.failNext {
		f.failNext = false
		return ports.GPUSchedulingQueueUpdateResult{}, ports.ErrQueueStoreUnavailable
	}
	// Idempotency replay
	if idempotencyKey != "" {
		if existingID, ok := f.byUpdateKey[idempotencyKey]; ok {
			return ports.GPUSchedulingQueueUpdateResult{Queue: f.queues[existingID], IdempotentReplay: true}, nil
		}
	}
	q, ok := f.queues[id]
	if !ok {
		return ports.GPUSchedulingQueueUpdateResult{}, ports.ErrQueueNotFound
	}
	if f.platformDefaultIDs[id] {
		return ports.GPUSchedulingQueueUpdateResult{}, ports.ErrPlatformDefaultProtected
	}
	if req.Weight != nil {
		q.Weight = *req.Weight
	}
	if req.Reclaimable != nil {
		q.Reclaimable = *req.Reclaimable
	}
	if req.WorkloadClass != nil {
		q.WorkloadClass = *req.WorkloadClass
	}
	if req.ProjectID != nil {
		q.ProjectID = *req.ProjectID
	}
	q.UpdatedAt = time.Now().UTC()
	f.queues[id] = q
	if idempotencyKey != "" {
		f.byUpdateKey[idempotencyKey] = id
	}
	return ports.GPUSchedulingQueueUpdateResult{Queue: q}, nil
}

func (f *fakeQueueStore) Delete(ctx context.Context, tenantID, id string) error {
	if f.failNext {
		f.failNext = false
		return ports.ErrQueueStoreUnavailable
	}
	q, ok := f.queues[id]
	if !ok {
		return ports.ErrQueueNotFound
	}
	if f.platformDefaultIDs[id] {
		return ports.ErrPlatformDefaultProtected
	}
	delete(f.queues, id)
	delete(f.byName, tenantID+"|"+q.Name)
	return nil
}

func (f *fakeQueueStore) seedPlatformDefault(tenantID, name string) {
	now := time.Now().UTC()
	q := ports.GPUSchedulingQueue{
		ID:                "platform-" + name,
		Name:              name,
		Weight:            10,
		WorkloadClass:     ports.WorkloadClassInference,
		IsPlatformDefault: true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	f.queues[q.ID] = q
	f.byName[tenantID+"|"+name] = q.ID
	f.platformDefaultIDs[q.ID] = true
}

// --- Unit tests at API-method level ---

func TestGPUSchedulingAPIListQueues(t *testing.T) {
	store := newFakeQueueStore()
	_, err := store.Create(context.Background(), "tenant-a", "", ports.GPUSchedulingQueueCreateRequest{
		Name: "inference-a", Weight: 10, WorkloadClass: ports.WorkloadClassInference,
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	queues, err := store.List(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(queues) != 1 || queues[0].Name != "inference-a" {
		t.Fatalf("queues = %+v, want 1 inference-a", queues)
	}
}

func TestGPUSchedulingAPIQueueToResponse(t *testing.T) {
	now := time.Now().UTC()
	q := ports.GPUSchedulingQueue{
		ID:            "q-1",
		Name:          "test-queue",
		Weight:        5,
		Reclaimable:   true,
		WorkloadClass: ports.WorkloadClassTraining,
		ProjectID:     "proj-1",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	resp := queueToResponse(q)
	if resp.ID != "q-1" || resp.Name != "test-queue" || resp.Weight != 5 || !resp.Reclaimable {
		t.Fatalf("resp = %+v, want fields mapped", resp)
	}
	if resp.WorkloadClass != "training" || resp.ProjectID == nil || *resp.ProjectID != "proj-1" {
		t.Fatalf("resp = %+v, want workload_class=training project_id=proj-1", resp)
	}
}

// --- HTTP-level tests using Hertz test engine ---

func setupGPUSchedulingTestServer(store ports.GPUSchedulingQueueStore) *server.Hertz {
	h := server.Default()
	h.Use(middleware.RequestID())
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		tenantID := string(c.GetHeader("X-Dev-Tenant-ID"))
		if tenantID == "" {
			tenantID = "00000000-0000-0000-0000-000000000001"
		}
		c.Set("tenant_id", tenantID)
		c.Next(ctx)
	})
	v1 := h.Group("/api/v1")
	registerGPUSchedulingResourcesWithStore(v1, store)
	return h
}

func TestGPUSchedulingHTTPEndpoints(t *testing.T) {
	store := newFakeQueueStore()
	h := setupGPUSchedulingTestServer(store)

	// POST create
	createBody := `{"name":"inference-test","weight":10,"reclaimable":false,"workload_class":"inference"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/gpu-scheduling/queues",
		&ut.Body{Body: bytes.NewBufferString(createBody), Len: len(createBody)},
		ut.Header{Key: "Idempotency-Key", Value: "test-key-1"},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusCreated {
		t.Fatalf("POST status = %d, want 201", resp.StatusCode())
	}
	var created gpuSchedulingQueueResponse
	if err := json.Unmarshal(resp.Body(), &created); err != nil {
		t.Fatalf("decode created = %v", err)
	}
	if created.Name != "inference-test" || created.Weight != 10 {
		t.Fatalf("created = %+v, want inference-test weight=10", created)
	}

	// GET list
	resp = ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/gpu-scheduling/queues",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("GET list status = %d, want 200", resp.StatusCode())
	}
	var list gpuSchedulingQueueListResponse
	if err := json.Unmarshal(resp.Body(), &list); err != nil {
		t.Fatalf("decode list = %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "inference-test" {
		t.Fatalf("list = %+v, want 1 inference-test", list)
	}

	// GET by id
	resp = ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/gpu-scheduling/queues/"+created.ID,
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("GET by id status = %d, want 200", resp.StatusCode())
	}
	var got gpuSchedulingQueueResponse
	if err := json.Unmarshal(resp.Body(), &got); err != nil {
		t.Fatalf("decode get = %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("got.ID = %s, want %s", got.ID, created.ID)
	}

	// PATCH update
	patchBody := `{"weight":20}`
	resp = ut.PerformRequest(h.Engine, http.MethodPatch, "/api/v1/gpu-scheduling/queues/"+created.ID,
		&ut.Body{Body: bytes.NewBufferString(patchBody), Len: len(patchBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
		ut.Header{Key: "Idempotency-Key", Value: "patch-key-1"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("PATCH status = %d, want 200", resp.StatusCode())
	}
	var updated gpuSchedulingQueueResponse
	if err := json.Unmarshal(resp.Body(), &updated); err != nil {
		t.Fatalf("decode updated = %v", err)
	}
	if updated.Weight != 20 {
		t.Fatalf("updated.Weight = %d, want 20", updated.Weight)
	}

	// DELETE
	resp = ut.PerformRequest(h.Engine, http.MethodDelete, "/api/v1/gpu-scheduling/queues/"+created.ID,
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", resp.StatusCode())
	}
}

func TestGPUSchedulingHTTPCreateRequiresIdempotencyKey(t *testing.T) {
	store := newFakeQueueStore()
	h := setupGPUSchedulingTestServer(store)

	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/gpu-scheduling/queues",
		&ut.Body{Body: bytes.NewBufferString(`{"name":"test","weight":1,"workload_class":"inference"}`), Len: 47},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("POST without Idempotency-Key status = %d, want 400", resp.StatusCode())
	}
}

func TestGPUSchedulingHTTPPlatformDefaultProtected(t *testing.T) {
	store := newFakeQueueStore()
	store.seedPlatformDefault("tenant-a", "ani-inference")
	h := setupGPUSchedulingTestServer(store)

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/gpu-scheduling/queues",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusOK {
		t.Fatalf("GET status = %d, want 200", resp.StatusCode())
	}
	var list gpuSchedulingQueueListResponse
	if err := json.Unmarshal(resp.Body(), &list); err != nil {
		t.Fatalf("decode list = %v", err)
	}
	if len(list.Items) != 1 || !list.Items[0].IsPlatformDefault {
		t.Fatalf("list = %+v, want 1 platform default", list)
	}
	queueID := list.Items[0].ID

	patchBody := `{"weight":99}`
	resp = ut.PerformRequest(h.Engine, http.MethodPatch, "/api/v1/gpu-scheduling/queues/"+queueID,
		&ut.Body{Body: bytes.NewBufferString(patchBody), Len: len(patchBody)},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
		ut.Header{Key: "Idempotency-Key", Value: "patch-key-2"},
	).Result()
	if resp.StatusCode() != http.StatusForbidden {
		t.Fatalf("PATCH platform default status = %d, want 403", resp.StatusCode())
	}

	resp = ut.PerformRequest(h.Engine, http.MethodDelete, "/api/v1/gpu-scheduling/queues/"+queueID,
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusForbidden {
		t.Fatalf("DELETE platform default status = %d, want 403", resp.StatusCode())
	}
}

func TestGPUSchedulingHTTPQueueNotFound(t *testing.T) {
	store := newFakeQueueStore()
	h := setupGPUSchedulingTestServer(store)

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/gpu-scheduling/queues/nonexistent",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusNotFound {
		t.Fatalf("GET nonexistent status = %d, want 404", resp.StatusCode())
	}
}

func TestGPUSchedulingHTTPQueueNameConflict(t *testing.T) {
	store := newFakeQueueStore()
	h := setupGPUSchedulingTestServer(store)

	body := `{"name":"dup","weight":1,"workload_class":"inference"}`
	_ = ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/gpu-scheduling/queues",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Idempotency-Key", Value: "key-1"},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/gpu-scheduling/queues",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Idempotency-Key", Value: "key-2"},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusConflict {
		t.Fatalf("duplicate POST status = %d, want 409", resp.StatusCode())
	}
}

func TestGPUSchedulingHTTPStoreUnavailable(t *testing.T) {
	store := newFakeQueueStore()
	store.failNext = true
	h := setupGPUSchedulingTestServer(store)

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/gpu-scheduling/queues",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("GET with failing store status = %d, want 503", resp.StatusCode())
	}
}

func TestGPUSchedulingHTTPNilStoreReturns503(t *testing.T) {
	h := setupGPUSchedulingTestServer(nil)

	resp := ut.PerformRequest(h.Engine, http.MethodGet, "/api/v1/gpu-scheduling/queues",
		nil,
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("GET with nil store status = %d, want 503", resp.StatusCode())
	}
}

func TestGPUSchedulingHTTPCreateInvalidBody(t *testing.T) {
	store := newFakeQueueStore()
	h := setupGPUSchedulingTestServer(store)

	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/gpu-scheduling/queues",
		&ut.Body{Body: bytes.NewBufferString(`not json`), Len: 8},
		ut.Header{Key: "Idempotency-Key", Value: "key-1"},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("POST invalid body status = %d, want 400", resp.StatusCode())
	}
}

func TestGPUSchedulingHTTPCreateMissingName(t *testing.T) {
	store := newFakeQueueStore()
	h := setupGPUSchedulingTestServer(store)

	body := `{"weight":1,"workload_class":"inference"}`
	resp := ut.PerformRequest(h.Engine, http.MethodPost, "/api/v1/gpu-scheduling/queues",
		&ut.Body{Body: bytes.NewBufferString(body), Len: len(body)},
		ut.Header{Key: "Idempotency-Key", Value: "key-1"},
		ut.Header{Key: "Content-Type", Value: "application/json"},
		ut.Header{Key: "X-Dev-Tenant-ID", Value: "tenant-a"},
	).Result()
	if resp.StatusCode() != http.StatusBadRequest {
		t.Fatalf("POST missing name status = %d, want 400", resp.StatusCode())
	}
}

func TestGPUSchedulingWriteErrorMapping(t *testing.T) {
	c := &app.RequestContext{}
	cases := []struct {
		err      error
		wantCode int
	}{
		{ports.ErrQueueNotFound, http.StatusNotFound},
		{ports.ErrQueueNameConflict, http.StatusConflict},
		{ports.ErrPlatformDefaultProtected, http.StatusForbidden},
		{ports.ErrQueueStoreUnavailable, http.StatusServiceUnavailable},
		{ports.ErrInvalid, http.StatusBadRequest},
		{errors.New("unknown"), http.StatusBadRequest},
	}
	for _, tc := range cases {
		writeGPUSchedulingError(c, tc.err)
	}
}
