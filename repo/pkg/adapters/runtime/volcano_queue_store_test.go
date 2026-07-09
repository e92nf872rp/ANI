package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

// fakeK8sAPI simulates the Volcano Queue CRD subset of the K8s API server.
// It stores queues in memory keyed by CRD name and enforces labelSelector filters.
type fakeK8sAPI struct {
	queues  map[string]volcanoQueueCRD // keyed by metadata.name
	now     func() time.Time
	tenant  string // label value used for created records
	failNext bool
}

func newFakeK8sAPI() *fakeK8sAPI {
	return &fakeK8sAPI{
		queues: map[string]volcanoQueueCRD{},
		now:    func() time.Time { return time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC) },
	}
}

func (f *fakeK8sAPI) Do(ctx context.Context, method, endpoint, contentType string, body []byte) ([]byte, int, error) {
	if f.failNext {
		f.failNext = false
		return nil, 0, errors.New("connection refused")
	}
	switch method {
	case http.MethodGet:
		return f.handleGet(endpoint)
	case http.MethodPost:
		return f.handlePost(endpoint, body)
	case http.MethodPut:
		return f.handlePut(endpoint, body)
	case http.MethodDelete:
		return f.handleDelete(endpoint)
	default:
		return nil, http.StatusBadRequest, fmt.Errorf("unsupported method: %s", method)
	}
}

func (f *fakeK8sAPI) handleGet(endpoint string) ([]byte, int, error) {
	labelSelector := extractLabelSelector(endpoint)
	if strings.Contains(endpoint, "/queues/") {
		// single resource GET by name
		name := extractResourceName(endpoint)
		crd, ok := f.queues[name]
		if !ok {
			return k8sStatusJSON(http.StatusNotFound, "queues.scheduling.volcano.sh \""+name+"\" not found"), http.StatusNotFound, nil
		}
		if !labelSelectorMatches(crd, labelSelector) {
			return k8sStatusJSON(http.StatusNotFound, "not found"), http.StatusNotFound, nil
		}
		body, _ := json.Marshal(crd)
		return body, http.StatusOK, nil
	}
	// collection GET
	items := make([]volcanoQueueCRD, 0)
	for _, crd := range f.queues {
		if labelSelectorMatches(crd, labelSelector) {
			items = append(items, crd)
		}
	}
	list := volcanoQueueListCRD{
		APIVersion: volcanoQueueAPIGroup + "/" + volcanoQueueAPIVersion,
		Kind:       "QueueList",
		Items:      items,
	}
	body, _ := json.Marshal(list)
	return body, http.StatusOK, nil
}

func (f *fakeK8sAPI) handlePost(endpoint string, body []byte) ([]byte, int, error) {
	var crd volcanoQueueCRD
	if err := json.Unmarshal(body, &crd); err != nil {
		return k8sStatusJSON(http.StatusBadRequest, "invalid body"), http.StatusBadRequest, nil
	}
	if _, exists := f.queues[crd.Metadata.Name]; exists {
		return k8sStatusJSON(http.StatusConflict, "queues.scheduling.volcano.sh \""+crd.Metadata.Name+"\" already exists"), http.StatusConflict, nil
	}
	if crd.Metadata.Labels == nil {
		crd.Metadata.Labels = map[string]string{}
	}
	if crd.Metadata.Annotations == nil {
		crd.Metadata.Annotations = map[string]string{}
	}
	f.queues[crd.Metadata.Name] = crd
	resp, _ := json.Marshal(crd)
	return resp, http.StatusCreated, nil
}

func (f *fakeK8sAPI) handlePut(endpoint string, body []byte) ([]byte, int, error) {
	var crd volcanoQueueCRD
	if err := json.Unmarshal(body, &crd); err != nil {
		return k8sStatusJSON(http.StatusBadRequest, "invalid body"), http.StatusBadRequest, nil
	}
	name := extractResourceName(endpoint)
	if _, exists := f.queues[name]; !exists {
		return k8sStatusJSON(http.StatusNotFound, "not found"), http.StatusNotFound, nil
	}
	existing := f.queues[name]
	if crd.Metadata.Labels == nil {
		crd.Metadata.Labels = existing.Metadata.Labels
	}
	if crd.Metadata.Annotations == nil {
		crd.Metadata.Annotations = existing.Metadata.Annotations
	}
	f.queues[name] = crd
	resp, _ := json.Marshal(crd)
	return resp, http.StatusOK, nil
}

func (f *fakeK8sAPI) handleDelete(endpoint string) ([]byte, int, error) {
	name := extractResourceName(endpoint)
	if _, exists := f.queues[name]; !exists {
		return k8sStatusJSON(http.StatusNotFound, "not found"), http.StatusNotFound, nil
	}
	delete(f.queues, name)
	return nil, http.StatusNoContent, nil
}

func (f *fakeK8sAPI) seedPlatformDefault(tenantID, name string) {
	crd := volcanoQueueCRD{
		APIVersion: volcanoQueueAPIGroup + "/" + volcanoQueueAPIVersion,
		Kind:       volcanoQueueKind,
		Metadata: volcanoQueueCRDMeta{
			Name:      name,
			Namespace: "volcano-system",
			Labels: map[string]string{
				volcanoLabelTenantID:        tenantID,
				volcanoLabelWorkloadClass:   string(ports.WorkloadClassInference),
				volcanoLabelQueueID:         "platform-default-" + name,
				volcanoLabelPlatformDefault: "true",
			},
			Annotations: map[string]string{
				"ani.kubercloud.io/created-at": f.now().Format(time.RFC3339),
				"ani.kubercloud.io/updated-at": f.now().Format(time.RFC3339),
			},
		},
		Spec: volcanoQueueCRDSpec{Weight: 10, Reclaimable: false},
	}
	f.queues[name] = crd
}

func (f *fakeK8sAPI) seedTenantQueue(tenantID, name, queueID string, workloadClass ports.WorkloadClass) {
	crd := volcanoQueueCRD{
		APIVersion: volcanoQueueAPIGroup + "/" + volcanoQueueAPIVersion,
		Kind:       volcanoQueueKind,
		Metadata: volcanoQueueCRDMeta{
			Name:      name,
			Namespace: "volcano-system",
			Labels: map[string]string{
				volcanoLabelTenantID:      tenantID,
				volcanoLabelWorkloadClass: string(workloadClass),
				volcanoLabelQueueID:       queueID,
			},
			Annotations: map[string]string{
				"ani.kubercloud.io/created-at": f.now().Format(time.RFC3339),
				"ani.kubercloud.io/updated-at": f.now().Format(time.RFC3339),
			},
		},
		Spec: volcanoQueueCRDSpec{Weight: 5, Reclaimable: true},
	}
	f.queues[name] = crd
}

func extractLabelSelector(endpoint string) string {
	idx := strings.Index(endpoint, "labelSelector=")
	if idx < 0 {
		return ""
	}
	return endpoint[idx+len("labelSelector="):]
}

func extractResourceName(endpoint string) string {
	parts := strings.Split(strings.TrimRight(endpoint, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	// strip query string
	name := parts[len(parts)-1]
	if idx := strings.Index(name, "?"); idx >= 0 {
		name = name[:idx]
	}
	return name
}

// labelSelectorMatches returns true when all key=value pairs in selector
// are present in crd labels.
func labelSelectorMatches(crd volcanoQueueCRD, selector string) bool {
	if selector == "" {
		return true
	}
	for _, pair := range strings.Split(selector, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		if crd.Metadata.Labels[kv[0]] != kv[1] {
			return false
		}
	}
	return true
}

func k8sStatusJSON(code int, message string) []byte {
	body, _ := json.Marshal(map[string]any{
		"kind":    "Status",
		"status":  "Failure",
		"code":    code,
		"message": message,
	})
	return body
}

func newTestStore(api *fakeK8sAPI) *VolcanoQueueStore {
	return NewVolcanoQueueStore(VolcanoQueueStoreConfig{
		Doer:      api,
		BaseURL:   "https://kubernetes.default.svc",
		Namespace: "volcano-system",
		Now:       api.now,
	})
}

func TestVolcanoQueueStoreCreateAndList(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	created, err := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name:          "inference-a",
		Weight:        10,
		Reclaimable:   false,
		WorkloadClass: ports.WorkloadClassInference,
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if created.ID == "" || created.Name != "inference-a" {
		t.Fatalf("created = %+v, want ID and Name=inference-a", created)
	}

	queues, err := store.List(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(queues) != 1 || queues[0].Name != "inference-a" {
		t.Fatalf("queues = %+v, want 1 queue named inference-a", queues)
	}
}

func TestVolcanoQueueStoreGet(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	created, _ := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "training-a", Weight: 20, WorkloadClass: ports.WorkloadClassTraining,
	})
	got, err := store.Get(context.Background(), "tenant-a", created.ID)
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if got.Name != "training-a" || got.Weight != 20 {
		t.Fatalf("got = %+v, want training-a weight=20", got)
	}
}

func TestVolcanoQueueStoreUpdate(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	created, _ := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "batch-a", Weight: 5, WorkloadClass: ports.WorkloadClassBatch,
	})
	newWeight := 15
	updated, err := store.Update(context.Background(), "tenant-a", created.ID, ports.GPUSchedulingQueueUpdateRequest{
		Weight: &newWeight,
	})
	if err != nil {
		t.Fatalf("Update error = %v", err)
	}
	if updated.Weight != 15 {
		t.Fatalf("updated.Weight = %d, want 15", updated.Weight)
	}
}

func TestVolcanoQueueStoreDelete(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	created, err := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "temp-a", Weight: 1, WorkloadClass: ports.WorkloadClassInference,
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if err := store.Delete(context.Background(), "tenant-a", created.ID); err != nil {
		t.Fatalf("Delete error = %v", err)
	}
	_, err = store.Get(context.Background(), "tenant-a", created.ID)
	if !errors.Is(err, ports.ErrQueueNotFound) {
		t.Fatalf("Get after delete error = %v, want ErrQueueNotFound", err)
	}
}

func TestVolcanoQueueStoreTenantIsolation(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	_, _ = store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "queue-a", Weight: 10, WorkloadClass: ports.WorkloadClassInference,
	})
	queuesB, err := store.List(context.Background(), "tenant-b")
	if err != nil {
		t.Fatalf("List tenant-b error = %v", err)
	}
	if len(queuesB) != 0 {
		t.Fatalf("tenant-b queues = %+v, want 0 (tenant isolation)", queuesB)
	}
}

func TestVolcanoQueueStoreCrossTenantGetReturnsNotFound(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	created, _ := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "secret-a", Weight: 1, WorkloadClass: ports.WorkloadClassInference,
	})
	_, err := store.Get(context.Background(), "tenant-b", created.ID)
	if !errors.Is(err, ports.ErrQueueNotFound) {
		t.Fatalf("cross-tenant Get error = %v, want ErrQueueNotFound", err)
	}
}

func TestVolcanoQueueStoreCrossTenantDeleteReturnsNotFound(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	created, _ := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "protected-a", Weight: 1, WorkloadClass: ports.WorkloadClassInference,
	})
	err := store.Delete(context.Background(), "tenant-b", created.ID)
	if !errors.Is(err, ports.ErrQueueNotFound) {
		t.Fatalf("cross-tenant Delete error = %v, want ErrQueueNotFound", err)
	}
}

func TestVolcanoQueueStorePlatformDefaultProtected(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)
	api.seedPlatformDefault("tenant-a", "ani-inference")

	queues, _ := store.List(context.Background(), "tenant-a")
	if len(queues) != 1 || !queues[0].IsPlatformDefault {
		t.Fatalf("queues = %+v, want 1 platform default", queues)
	}
	queueID := queues[0].ID

	_, err := store.Update(context.Background(), "tenant-a", queueID, ports.GPUSchedulingQueueUpdateRequest{
		Weight: intPtr(99),
	})
	if !errors.Is(err, ports.ErrPlatformDefaultProtected) {
		t.Fatalf("Update platform default error = %v, want ErrPlatformDefaultProtected", err)
	}

	err = store.Delete(context.Background(), "tenant-a", queueID)
	if !errors.Is(err, ports.ErrPlatformDefaultProtected) {
		t.Fatalf("Delete platform default error = %v, want ErrPlatformDefaultProtected", err)
	}
}

func TestVolcanoQueueStoreCreateNameConflict(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	_, _ = store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "dup-a", Weight: 1, WorkloadClass: ports.WorkloadClassInference,
	})
	_, err := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "dup-a", Weight: 1, WorkloadClass: ports.WorkloadClassInference,
	})
	if !errors.Is(err, ports.ErrQueueNameConflict) {
		t.Fatalf("duplicate Create error = %v, want ErrQueueNameConflict", err)
	}
}

func TestVolcanoQueueStoreCreateInvalidName(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	cases := []string{"", "UPPER", "has spaces", "-leading", "trailing-", strings.Repeat("a", 64)}
	for _, name := range cases {
		_, err := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
			Name: name, Weight: 1, WorkloadClass: ports.WorkloadClassInference,
		})
		if err == nil {
			t.Errorf("Create name=%q succeeded, want error", name)
		}
	}
}

func TestVolcanoQueueStoreUnavailableWhenDoerNil(t *testing.T) {
	store := NewVolcanoQueueStore(VolcanoQueueStoreConfig{})
	_, err := store.List(context.Background(), "tenant-a")
	if !errors.Is(err, ports.ErrQueueStoreUnavailable) {
		t.Fatalf("List with nil doer error = %v, want ErrQueueStoreUnavailable", err)
	}
}

func TestVolcanoQueueStoreUnavailableOnConnectionRefused(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)
	api.failNext = true

	_, err := store.List(context.Background(), "tenant-a")
	if !errors.Is(err, ports.ErrQueueStoreUnavailable) {
		t.Fatalf("List error = %v, want ErrQueueStoreUnavailable", err)
	}
}

func TestVolcanoQueueStoreCreateSameNameDifferentTenantStillConflicts(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	_, err := store.Create(context.Background(), "tenant-a", ports.GPUSchedulingQueueCreateRequest{
		Name: "shared-name", Weight: 1, WorkloadClass: ports.WorkloadClassInference,
	})
	if err != nil {
		t.Fatalf("Create tenant-a error = %v", err)
	}
	// Volcano Queue CRD is cluster-scoped in volcano-system namespace, so the
	// same CRD name collides at the K8s API level even across tenants. The
	// adapter's List-based uniqueness check is tenant-scoped, but the POST
	// surfaces the K8s 409 as ErrQueueNameConflict. Tenant prefixing of queue
	// names is the production mitigation (SPEC §5.1 Create step 2).
	_, err = store.Create(context.Background(), "tenant-b", ports.GPUSchedulingQueueCreateRequest{
		Name: "shared-name", Weight: 1, WorkloadClass: ports.WorkloadClassInference,
	})
	if !errors.Is(err, ports.ErrQueueNameConflict) {
		t.Fatalf("Create tenant-b same name error = %v, want ErrQueueNameConflict", err)
	}
}

func TestVolcanoQueueStoreGetNonexistentReturnsNotFound(t *testing.T) {
	api := newFakeK8sAPI()
	store := newTestStore(api)

	_, err := store.Get(context.Background(), "tenant-a", "nonexistent-id")
	if !errors.Is(err, ports.ErrQueueNotFound) {
		t.Fatalf("Get nonexistent error = %v, want ErrQueueNotFound", err)
	}
}

func intPtr(v int) *int { return &v }
