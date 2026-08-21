package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

// fakeGPUSpecStore is an in-memory ports.GPUSpecStore for translator tests.
type fakeGPUSpecStore struct {
	specs map[string]ports.GPUSpecCRD
}

func newFakeGPUSpecStore() *fakeGPUSpecStore {
	return &fakeGPUSpecStore{specs: map[string]ports.GPUSpecCRD{}}
}

func (f *fakeGPUSpecStore) List(ctx context.Context) ([]ports.GPUSpecCRD, error) {
	result := make([]ports.GPUSpecCRD, 0, len(f.specs))
	for _, s := range f.specs {
		result = append(result, s)
	}
	return result, nil
}

func (f *fakeGPUSpecStore) Get(ctx context.Context, specID string) (ports.GPUSpecCRD, error) {
	spec, ok := f.specs[specID]
	if !ok {
		return ports.GPUSpecCRD{}, ports.ErrGPUSpecNotFound
	}
	return spec, nil
}

func (f *fakeGPUSpecStore) Create(ctx context.Context, idempotencyKey string, spec ports.GPUSpecCRD) (ports.GPUSpecCRD, error) {
	f.specs[spec.ID] = spec
	return spec, nil
}

func (f *fakeGPUSpecStore) Delete(ctx context.Context, idempotencyKey string, specID string) error {
	delete(f.specs, specID)
	return nil
}

func wholecardSpec() ports.GPUSpecCRD {
	return ports.GPUSpecCRD{
		ID:         "nvidia-a100-sxm4-80gb",
		GPUType:    "NVIDIA-A100-SXM4-80GB",
		GPUMode:    "wholecard",
		Shares:     1,
		MBPerShare: 80640,
		NodeAffinity: ports.GPUSpecNodeAffinity{
			GPUSpec: "NVIDIA-A100-SXM4-80GB",
			GPUMode: "wholecard",
		},
		VolcanoResources: ports.GPUSpecVolcanoResources{
			Wholecard: map[string]string{
				"nvidia.com/gpu": "{count}",
			},
		},
	}
}

func vgpuSpec() ports.GPUSpecCRD {
	return ports.GPUSpecCRD{
		ID:         "nvidia-a100-sxm4-80gb-vgpu-half",
		GPUType:    "NVIDIA-A100-SXM4-80GB-HALF",
		GPUMode:    "vgpu",
		Shares:     2,
		MBPerShare: 40320,
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

func TestVolcanoTranslator_WholecardSpecToPod(t *testing.T) {
	store := newFakeGPUSpecStore()
	store.specs["nvidia-a100-sxm4-80gb"] = wholecardSpec()
	translator := NewVolcanoResourceTranslator(store)

	result, err := translator.Translate(context.Background(), "nvidia-a100-sxm4-80gb", "ani-inference", 2)
	if err != nil {
		t.Fatalf("Translate error = %v", err)
	}
	// schedulerName must be volcano
	if result.SchedulerName != "volcano" {
		t.Fatalf("SchedulerName = %q, want volcano", result.SchedulerName)
	}
	// nodeSelector: gpu-mode + gpu-spec
	if result.NodeSelector["ani.kubercloud.io/gpu-mode"] != "wholecard" {
		t.Fatalf("gpu-mode = %q, want wholecard", result.NodeSelector["ani.kubercloud.io/gpu-mode"])
	}
	if result.NodeSelector["ani.kubercloud.io/gpu-spec"] != "NVIDIA-A100-SXM4-80GB" {
		t.Fatalf("gpu-spec = %q, want NVIDIA-A100-SXM4-80GB", result.NodeSelector["ani.kubercloud.io/gpu-spec"])
	}
	// vGPU labels must NOT be present in wholecard mode
	if _, ok := result.NodeSelector["ani.kubercloud.io/gpu-sharing-spec"]; ok {
		t.Fatalf("gpu-sharing-spec should not be set in wholecard mode")
	}
	if _, ok := result.NodeSelector["ani.kubercloud.io/gpu-sharing-policy"]; ok {
		t.Fatalf("gpu-sharing-policy should not be set in wholecard mode")
	}
	// resourceRequests: nvidia.com/gpu = "2" (count formatted)
	if result.ResourceRequests["nvidia.com/gpu"] != "2" {
		t.Fatalf("nvidia.com/gpu = %q, want 2", result.ResourceRequests["nvidia.com/gpu"])
	}
	// vGPU resources must NOT be present in wholecard mode
	if _, ok := result.ResourceRequests["volcano.sh/vgpu-memory"]; ok {
		t.Fatalf("volcano.sh/vgpu-memory should not be set in wholecard mode")
	}
	// annotation: queue-name
	if result.Annotations["scheduling.volcano.sh/queue-name"] != "ani-inference" {
		t.Fatalf("queue annotation = %q, want ani-inference", result.Annotations["scheduling.volcano.sh/queue-name"])
	}
}

func TestVolcanoTranslator_VGPUSpecToPod(t *testing.T) {
	store := newFakeGPUSpecStore()
	store.specs["nvidia-a100-sxm4-80gb-vgu-half"] = vgpuSpec()
	store.specs["nvidia-a100-sxm4-80gb-vgu-half"] = vgpuSpec()
	translator := NewVolcanoResourceTranslator(store)

	result, err := translator.Translate(context.Background(), "nvidia-a100-sxm4-80gb-vgu-half", "ani-training", 3)
	if err != nil {
		t.Fatalf("Translate error = %v", err)
	}
	// nodeSelector: gpu-mode + gpu-sharing-spec + gpu-sharing-policy
	if result.NodeSelector["ani.kubercloud.io/gpu-mode"] != "vgpu" {
		t.Fatalf("gpu-mode = %q, want vgpu", result.NodeSelector["ani.kubercloud.io/gpu-mode"])
	}
	if result.NodeSelector["ani.kubercloud.io/gpu-sharing-spec"] != "NVIDIA-A100-SXM4-80GB-HALF" {
		t.Fatalf("gpu-sharing-spec = %q, want NVIDIA-A100-SXM4-80GB-HALF", result.NodeSelector["ani.kubercloud.io/gpu-sharing-spec"])
	}
	if result.NodeSelector["ani.kubercloud.io/gpu-sharing-policy"] != "half" {
		t.Fatalf("gpu-sharing-policy = %q, want half", result.NodeSelector["ani.kubercloud.io/gpu-sharing-policy"])
	}
	// gpu-spec must NOT be present in vGPU mode
	if _, ok := result.NodeSelector["ani.kubercloud.io/gpu-spec"]; ok {
		t.Fatalf("gpu-spec should not be set in vGPU mode")
	}
	// resourceRequests: vgpu-memory = MBPerShare/factor (40320/10=4032), vgpu-number = count (3)
	if result.ResourceRequests["volcano.sh/vgpu-memory"] != "4032" {
		t.Fatalf("volcano.sh/vgpu-memory = %q, want 4032", result.ResourceRequests["volcano.sh/vgpu-memory"])
	}
	if result.ResourceRequests["volcano.sh/vgpu-number"] != "3" {
		t.Fatalf("volcano.sh/vgpu-number = %q, want 3", result.ResourceRequests["volcano.sh/vgpu-number"])
	}
	// wholecard resources must NOT be present in vGPU mode
	if _, ok := result.ResourceRequests["nvidia.com/gpu"]; ok {
		t.Fatalf("nvidia.com/gpu should not be set in vGPU mode")
	}
}

func TestVolcanoTranslator_VGPUMemoryNeverEmpty(t *testing.T) {
	store := newFakeGPUSpecStore()
	// vGPU spec with empty vgpu-memory template — adapter must substitute MBPerShare/factor
	spec := vgpuSpec()
	spec.MBPerShare = 20160
	spec.VolcanoResources.VGPU["volcano.sh/vgpu-memory"] = ""
	store.specs["vgpu-empty-mem"] = spec
	translator := NewVolcanoResourceTranslator(store)

	result, err := translator.Translate(context.Background(), "vgpu-empty-mem", "q", 1)
	if err != nil {
		t.Fatalf("Translate error = %v", err)
	}
	if result.ResourceRequests["volcano.sh/vgpu-memory"] != "2016" {
		t.Fatalf("volcano.sh/vgpu-memory = %q, want 2016 (must never be empty)", result.ResourceRequests["volcano.sh/vgpu-memory"])
	}
}

func TestVolcanoTranslator_SpecNotFound(t *testing.T) {
	store := newFakeGPUSpecStore()
	translator := NewVolcanoResourceTranslator(store)

	_, err := translator.Translate(context.Background(), "nonexistent-spec", "q", 1)
	if !errors.Is(err, ports.ErrGPUSpecNotFound) {
		t.Fatalf("Translate nonexistent error = %v, want ErrGPUSpecNotFound", err)
	}
}

func TestVolcanoTranslator_InvalidCount(t *testing.T) {
	store := newFakeGPUSpecStore()
	store.specs["s"] = wholecardSpec()
	translator := NewVolcanoResourceTranslator(store)

	_, err := translator.Translate(context.Background(), "s", "q", 0)
	if err == nil {
		t.Fatalf("Translate count=0 should return error")
	}
	_, err = translator.Translate(context.Background(), "s", "q", -1)
	if err == nil {
		t.Fatalf("Translate count=-1 should return error")
	}
}

func TestVolcanoTranslator_QueueAnnotation(t *testing.T) {
	store := newFakeGPUSpecStore()
	store.specs["s"] = wholecardSpec()
	translator := NewVolcanoResourceTranslator(store)

	result, _ := translator.Translate(context.Background(), "s", "my-queue", 1)
	if result.Annotations["scheduling.volcano.sh/queue-name"] != "my-queue" {
		t.Fatalf("queue annotation = %q, want my-queue", result.Annotations["scheduling.volcano.sh/queue-name"])
	}
}

func TestVolcanoTranslator_HuaweiWholecard(t *testing.T) {
	store := newFakeGPUSpecStore()
	spec := ports.GPUSpecCRD{
		ID:         "ascend-910",
		GPUType:    "Ascend-910",
		GPUMode:    "wholecard",
		Shares:     1,
		MBPerShare: 32768,
		NodeAffinity: ports.GPUSpecNodeAffinity{
			GPUSpec: "Ascend-910",
			GPUMode: "wholecard",
		},
		VolcanoResources: ports.GPUSpecVolcanoResources{
			Wholecard: map[string]string{
				"huawei.com/Ascend910": "{count}",
			},
		},
	}
	store.specs["ascend-910"] = spec
	translator := NewVolcanoResourceTranslator(store)

	result, err := translator.Translate(context.Background(), "ascend-910", "q", 4)
	if err != nil {
		t.Fatalf("Translate error = %v", err)
	}
	if result.ResourceRequests["huawei.com/Ascend910"] != "4" {
		t.Fatalf("huawei.com/Ascend910 = %q, want 4", result.ResourceRequests["huawei.com/Ascend910"])
	}
	if result.NodeSelector["ani.kubercloud.io/gpu-spec"] != "Ascend-910" {
		t.Fatalf("gpu-spec = %q, want Ascend-910", result.NodeSelector["ani.kubercloud.io/gpu-spec"])
	}
}

func TestVolcanoTranslator_GPUModeIsolation(t *testing.T) {
	store := newFakeGPUSpecStore()
	store.specs["wc"] = wholecardSpec()
	store.specs["vg"] = vgpuSpec()
	translator := NewVolcanoResourceTranslator(store)

	wcResult, _ := translator.Translate(context.Background(), "wc", "q", 1)
	vgResult, _ := translator.Translate(context.Background(), "vg", "q", 1)
	// Both must have gpu-mode set but to different values
	if wcResult.NodeSelector["ani.kubercloud.io/gpu-mode"] != "wholecard" {
		t.Fatalf("wholecard gpu-mode = %q, want wholecard", wcResult.NodeSelector["ani.kubercloud.io/gpu-mode"])
	}
	if vgResult.NodeSelector["ani.kubercloud.io/gpu-mode"] != "vgpu" {
		t.Fatalf("vgpu gpu-mode = %q, want vgpu", vgResult.NodeSelector["ani.kubercloud.io/gpu-mode"])
	}
}
