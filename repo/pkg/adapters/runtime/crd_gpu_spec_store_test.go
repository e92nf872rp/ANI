package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// fakeGPUSpecAPI simulates the GPUSpec CRD subset of the K8s API server.
// It stores specs in memory keyed by CRD metadata.name and enforces
// labelSelector filters on list responses.
type fakeGPUSpecAPI struct {
	specs    map[string]gpuSpecCRD // keyed by metadata.name
	failNext bool
}

func newFakeGPUSpecAPI() *fakeGPUSpecAPI {
	return &fakeGPUSpecAPI{
		specs: map[string]gpuSpecCRD{},
	}
}

func (f *fakeGPUSpecAPI) Do(ctx context.Context, method, endpoint, contentType string, body []byte) ([]byte, int, error) {
	if f.failNext {
		f.failNext = false
		return nil, 0, errors.New("connection refused")
	}
	switch method {
	case http.MethodGet:
		return f.handleGPUSpecGet(endpoint)
	case http.MethodPost:
		return f.handleGPUSpecPost(endpoint, body)
	case http.MethodDelete:
		return f.handleGPUSpecDelete(endpoint)
	default:
		return nil, http.StatusBadRequest, fmt.Errorf("unsupported method: %s", method)
	}
}

func (f *fakeGPUSpecAPI) handleGPUSpecGet(endpoint string) ([]byte, int, error) {
	labelSelector := extractLabelSelector(endpoint)
	if strings.Contains(endpoint, "/gpuspecs/") {
		// single resource GET by name
		name := extractResourceName(endpoint)
		crd, ok := f.specs[name]
		if !ok {
			return k8sStatusJSON(http.StatusNotFound, "gpuspecs.ani.kubercloud.io \""+name+"\" not found"), http.StatusNotFound, nil
		}
		if !gpuSpecLabelSelectorMatches(crd, labelSelector) {
			return k8sStatusJSON(http.StatusNotFound, "not found"), http.StatusNotFound, nil
		}
		body, _ := json.Marshal(crd)
		return body, http.StatusOK, nil
	}
	// collection GET
	items := make([]gpuSpecCRD, 0)
	for _, crd := range f.specs {
		if gpuSpecLabelSelectorMatches(crd, labelSelector) {
			items = append(items, crd)
		}
	}
	list := gpuSpecListCRD{
		APIVersion: gpuSpecAPIGroup + "/" + gpuSpecAPIVersion,
		Kind:       gpuSpecListKind,
		Items:      items,
	}
	body, _ := json.Marshal(list)
	return body, http.StatusOK, nil
}

func (f *fakeGPUSpecAPI) handleGPUSpecPost(endpoint string, body []byte) ([]byte, int, error) {
	var crd gpuSpecCRD
	if err := json.Unmarshal(body, &crd); err != nil {
		return k8sStatusJSON(http.StatusBadRequest, "invalid body"), http.StatusBadRequest, nil
	}
	if _, exists := f.specs[crd.Metadata.Name]; exists {
		return k8sStatusJSON(http.StatusConflict, "gpuspecs.ani.kubercloud.io \""+crd.Metadata.Name+"\" already exists"), http.StatusConflict, nil
	}
	if crd.Metadata.Labels == nil {
		crd.Metadata.Labels = map[string]string{}
	}
	f.specs[crd.Metadata.Name] = crd
	resp, _ := json.Marshal(crd)
	return resp, http.StatusCreated, nil
}

func (f *fakeGPUSpecAPI) handleGPUSpecDelete(endpoint string) ([]byte, int, error) {
	name := extractResourceName(endpoint)
	if _, exists := f.specs[name]; !exists {
		return k8sStatusJSON(http.StatusNotFound, "not found"), http.StatusNotFound, nil
	}
	delete(f.specs, name)
	return nil, http.StatusNoContent, nil
}

// gpuSpecLabelSelectorMatches returns true when all key=value pairs in
// selector are present in crd labels.
func gpuSpecLabelSelectorMatches(crd gpuSpecCRD, selector string) bool {
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

func newGPUSpecTestStore(api *fakeGPUSpecAPI) *CRDGPUSpecStore {
	return NewCRDGPUSpecStore(CRDGPUSpecStoreConfig{
		Doer:    api,
		BaseURL: "https://kubernetes.default.svc",
	})
}

func sampleWholecardSpec() ports.GPUSpecCRD {
	return ports.GPUSpecCRD{
		ID:            "nvidia-a100-sxm4-80gb",
		Name:          "NVIDIA A100 SXM4 80GB",
		GPUType:       "NVIDIA-A100-SXM4-80GB",
		GPUMode:       "wholecard",
		MemoryTotalMB: 80640,
		Shares:        1,
		MBPerShare:    80640,
		Available:     true,
		NodeAffinity: ports.GPUSpecNodeAffinity{
			GPUSpec: "NVIDIA-A100-SXM4-80GB",
			GPUMode: "wholecard",
		},
		VolcanoResources: ports.GPUSpecVolcanoResources{
			Wholecard: map[string]string{"nvidia.com/gpu": "{count}"},
		},
	}
}

func sampleVGPUSpec() ports.GPUSpecCRD {
	return ports.GPUSpecCRD{
		ID:            "nvidia-a100-sxm4-80gb-vgpu-half",
		Name:          "NVIDIA A100 SXM4 80GB vGPU Half",
		GPUType:       "NVIDIA-A100-SXM4-80GB-HALF",
		GPUMode:       "vgpu",
		MemoryTotalMB: 80640,
		Shares:        2,
		MBPerShare:    40320,
		Available:     true,
		NodeAffinity: ports.GPUSpecNodeAffinity{
			GPUSharingSpec:   "NVIDIA-A100-SXM4-80GB-HALF",
			GPUSharingPolicy: "half",
			GPUMode:          "vgpu",
		},
		VolcanoResources: ports.GPUSpecVolcanoResources{
			VGPU: map[string]string{
				"volcano.sh/vgpu-memory": "{mb_per_share}",
				"volcano.sh/vgpu-number": "{count}",
			},
		},
	}
}

func TestGPUSpecStoreCreateAndList(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	created, err := store.Create(context.Background(), "create-key-1", sampleWholecardSpec())
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if created.ID != "nvidia-a100-sxm4-80gb" {
		t.Fatalf("created.ID = %q, want nvidia-a100-sxm4-80gb", created.ID)
	}
	if created.GPUType != "NVIDIA-A100-SXM4-80GB" {
		t.Fatalf("created.GPUType = %q, want NVIDIA-A100-SXM4-80GB", created.GPUType)
	}
	if created.GPUMode != "wholecard" {
		t.Fatalf("created.GPUMode = %q, want wholecard", created.GPUMode)
	}
	if !created.Available {
		t.Fatalf("created.Available = false, want true")
	}

	specs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(specs) != 1 || specs[0].ID != "nvidia-a100-sxm4-80gb" {
		t.Fatalf("specs = %+v, want 1 spec named nvidia-a100-sxm4-80gb", specs)
	}
}

func TestGPUSpecStoreGet(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	_, _ = store.Create(context.Background(), "create-key-2", sampleWholecardSpec())
	got, err := store.Get(context.Background(), "nvidia-a100-sxm4-80gb")
	if err != nil {
		t.Fatalf("Get error = %v", err)
	}
	if got.ID != "nvidia-a100-sxm4-80gb" || got.GPUType != "NVIDIA-A100-SXM4-80GB" {
		t.Fatalf("got = %+v, want nvidia-a100-sxm4-80gb / NVIDIA-A100-SXM4-80GB", got)
	}
}

func TestGPUSpecStoreGetNotFound(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	_, err := store.Get(context.Background(), "nonexistent-spec")
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("Get nonexistent error = %v, want ErrGPUSpecNotFound", err)
	}
}

func TestGPUSpecStoreDelete(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	_, _ = store.Create(context.Background(), "create-key-3", sampleWholecardSpec())
	if err := store.Delete(context.Background(), "delete-key-1", "nvidia-a100-sxm4-80gb"); err != nil {
		t.Fatalf("Delete error = %v", err)
	}
	_, err := store.Get(context.Background(), "nvidia-a100-sxm4-80gb")
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("Get after delete error = %v, want ErrGPUSpecNotFound", err)
	}
}

func TestGPUSpecStoreDeleteNotFound(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	err := store.Delete(context.Background(), "", "nonexistent-spec")
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("Delete nonexistent error = %v, want ErrGPUSpecNotFound", err)
	}
}

func TestGPUSpecStoreCreateIdempotencyReplay(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	spec := sampleWholecardSpec()
	first, err := store.Create(context.Background(), "idem-key-1", spec)
	if err != nil {
		t.Fatalf("Create first error = %v", err)
	}
	// Replay with same idempotency_key must return the existing CRD, not an error.
	replayed, err := store.Create(context.Background(), "idem-key-1", spec)
	if err != nil {
		t.Fatalf("Create replay error = %v, want nil (idempotent replay)", err)
	}
	if replayed.ID != first.ID {
		t.Fatalf("replayed.ID = %q, want %q (same CRD)", replayed.ID, first.ID)
	}
}

func TestGPUSpecStoreCreateConflict(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	_, _ = store.Create(context.Background(), "create-key-4", sampleWholecardSpec())
	// Same spec_id with a different idempotency_key must conflict.
	_, err := store.Create(context.Background(), "different-key", sampleWholecardSpec())
	if !errors.Is(err, ports.ErrGPUSpecConflict) {
		t.Fatalf("duplicate Create error = %v, want ErrGPUSpecConflict", err)
	}
}

func TestGPUSpecStoreCreateInvalidSpecID(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	cases := []string{"", "UPPER", "has spaces", "-leading", "trailing-", strings.Repeat("a", 64)}
	for _, id := range cases {
		spec := sampleWholecardSpec()
		spec.ID = id
		_, err := store.Create(context.Background(), "", spec)
		if err == nil {
			t.Errorf("Create spec_id=%q succeeded, want error", id)
		}
	}
}

func TestGPUSpecStoreVGPUSpecRoundTrip(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	created, err := store.Create(context.Background(), "create-key-5", sampleVGPUSpec())
	if err != nil {
		t.Fatalf("Create vGPU error = %v", err)
	}
	if created.GPUMode != "vgpu" {
		t.Fatalf("created.GPUMode = %q, want vgpu", created.GPUMode)
	}
	if created.Shares != 2 {
		t.Fatalf("created.Shares = %d, want 2", created.Shares)
	}
	if created.MBPerShare != 40320 {
		t.Fatalf("created.MBPerShare = %d, want 40320", created.MBPerShare)
	}
	if created.NodeAffinity.GPUSharingSpec != "NVIDIA-A100-SXM4-80GB-HALF" {
		t.Fatalf("NodeAffinity.GPUSharingSpec = %q, want NVIDIA-A100-SXM4-80GB-HALF", created.NodeAffinity.GPUSharingSpec)
	}
	if created.NodeAffinity.GPUSharingPolicy != "half" {
		t.Fatalf("NodeAffinity.GPUSharingPolicy = %q, want half", created.NodeAffinity.GPUSharingPolicy)
	}
	if created.VolcanoResources.VGPU["volcano.sh/vgpu-memory"] != "{mb_per_share}" {
		t.Fatalf("VolcanoResources.VGPU[vgpu-memory] = %q, want {mb_per_share}", created.VolcanoResources.VGPU["volcano.sh/vgpu-memory"])
	}
	if created.VolcanoResources.VGPU["volcano.sh/vgpu-number"] != "{count}" {
		t.Fatalf("VolcanoResources.VGPU[vgpu-number] = %q, want {count}", created.VolcanoResources.VGPU["volcano.sh/vgpu-number"])
	}

	got, err := store.Get(context.Background(), "nvidia-a100-sxm4-80gb-vgpu-half")
	if err != nil {
		t.Fatalf("Get vGPU error = %v", err)
	}
	if got.GPUMode != "vgpu" || got.Shares != 2 {
		t.Fatalf("got = %+v, want vgpu shares=2", got)
	}
}

func TestGPUSpecStoreListMultipleSpecs(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	_, _ = store.Create(context.Background(), "create-key-6", sampleWholecardSpec())
	_, _ = store.Create(context.Background(), "create-key-7", sampleVGPUSpec())
	specs, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("List error = %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("len(specs) = %d, want 2", len(specs))
	}
}

func TestGPUSpecStoreNilDoerReturnsNotFound(t *testing.T) {
	store := NewCRDGPUSpecStore(CRDGPUSpecStoreConfig{})
	_, err := store.List(context.Background())
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("List with nil doer error = %v, want ErrGPUSpecNotFound", err)
	}
	_, err = store.Get(context.Background(), "any-spec")
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("Get with nil doer error = %v, want ErrGPUSpecNotFound", err)
	}
	_, err = store.Create(context.Background(), "key", sampleWholecardSpec())
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("Create with nil doer error = %v, want ErrGPUSpecNotFound", err)
	}
	err = store.Delete(context.Background(), "", "any-spec")
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("Delete with nil doer error = %v, want ErrGPUSpecNotFound", err)
	}
}

func TestGPUSpecStoreConnectionRefused(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)
	api.failNext = true

	_, err := store.List(context.Background())
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("List error = %v, want ErrGPUSpecNotFound (connection refused mapped)", err)
	}
}

func TestGPUSpecStoreComputePerSharePreserved(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	spec := sampleWholecardSpec()
	compute := int64(50)
	spec.ComputePerShare = &compute
	created, err := store.Create(context.Background(), "create-key-8", spec)
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if created.ComputePerShare == nil || *created.ComputePerShare != 50 {
		t.Fatalf("created.ComputePerShare = %v, want 50", created.ComputePerShare)
	}
}

func TestGPUSpecStoreNameDerivedFromIDWhenEmpty(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	spec := sampleWholecardSpec()
	spec.Name = ""
	created, err := store.Create(context.Background(), "create-key-9", spec)
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if created.Name != "nvidia-a100-sxm4-80gb" {
		t.Fatalf("created.Name = %q, want derived from ID: nvidia-a100-sxm4-80gb", created.Name)
	}
}

func TestGPUSpecStoreAvailableFalsePreserved(t *testing.T) {
	api := newFakeGPUSpecAPI()
	store := newGPUSpecTestStore(api)

	spec := sampleWholecardSpec()
	spec.Available = false
	created, err := store.Create(context.Background(), "create-key-10", spec)
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if created.Available {
		t.Fatalf("created.Available = true, want false")
	}
}

// Ensure unused url import is referenced for builds that exclude some tests.
var _ = url.QueryEscape
