package runtime

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// Volcano scheduling constants (SPEC §5.1).
const (
	// volcanoSchedulerName is the fixed scheduler name that routes Pods
	// through the Volcano scheduler for queue admission control.
	volcanoSchedulerName = "volcano"

	// Node label keys for nodeSelector construction (plan.md §4.2).
	gpuModeLabelKey          = "ani.kubercloud.io/gpu-mode"
	gpuSpecLabelKey          = "ani.kubercloud.io/gpu-spec"
	gpuSharingSpecLabelKey   = "ani.kubercloud.io/gpu-sharing-spec"
	gpuSharingPolicyLabelKey = "ani.kubercloud.io/gpu-sharing-policy"

	// Volcano queue annotation key written into Pod metadata.
	volcanoQueueAnnotationKey = "scheduling.volcano.sh/queue-name"

	// Resource value placeholders supported in VolcanoResources maps.
	placeholderCount      = "{count}"
	placeholderMBPerShare = "{mb_per_share}"
)

// VolcanoTranslationResult is the output of VolcanoResourceTranslator.Translate.
// It carries the K8s Pod spec fragments needed to schedule a GPU workload
// through Volcano: node affinity, scheduler selection, resource requests
// and the queue annotation.
type VolcanoTranslationResult struct {
	NodeSelector     map[string]string
	SchedulerName    string
	ResourceRequests map[string]string
	Annotations      map[string]string
}

// VolcanoResourceTranslator translates a GPUSpec spec_id into K8s Pod spec
// fragments for Volcano scheduling (SPEC §5.1). It depends on GPUSpecStore
// to resolve the spec definition, then builds nodeSelector, schedulerName,
// resourceRequests and queue annotation according to the spec's gpu_mode
// and VolcanoResources declarations.
type VolcanoResourceTranslator struct {
	store ports.GPUSpecStore
}

// NewVolcanoResourceTranslator builds a translator backed by the given
// GPUSpecStore. The store must return ports.ErrGPUSpecNotFound for unknown
// spec_ids so Translate can surface a clear error.
func NewVolcanoResourceTranslator(store ports.GPUSpecStore) *VolcanoResourceTranslator {
	return &VolcanoResourceTranslator{store: store}
}

// Translate resolves the spec_id via GPUSpecStore and builds the K8s Pod
// spec fragments for Volcano scheduling.
//
// nodeSelector rules (plan.md §4.2):
//   - Common: ani.kubercloud.io/gpu-mode for physical isolation between
//     wholecard and vGPU nodes.
//   - Wholecard: ani.kubercloud.io/gpu-spec matching the GPU type.
//   - vGPU: ani.kubercloud.io/gpu-sharing-spec + ani.kubercloud.io/gpu-sharing-policy.
//
// resourceRequests rules (plan.md §4.1):
//   - Wholecard: entries from VolcanoResources.Wholecard (e.g. nvidia.com/gpu).
//   - vGPU: entries from VolcanoResources.VGPU; volcano.sh/vgpu-memory is
//     formatted with spec.MBPerShare and must never be empty.
func (t *VolcanoResourceTranslator) Translate(ctx context.Context, specID, queueName string, count int) (VolcanoTranslationResult, error) {
	if count <= 0 {
		return VolcanoTranslationResult{}, fmt.Errorf("%w: count must be >= 1, got %d", ports.ErrGPUSpecNotFound, count)
	}
	spec, err := t.store.Get(ctx, specID)
	if err != nil {
		return VolcanoTranslationResult{}, err
	}
	return VolcanoTranslationResult{
		NodeSelector:     buildNodeSelector(spec),
		SchedulerName:    volcanoSchedulerName,
		ResourceRequests: buildResourceRequests(spec, count),
		Annotations:      buildQueueAnnotation(queueName),
	}, nil
}

// buildNodeSelector constructs the nodeSelector map from the spec's
// NodeAffinity. The gpu-mode label is always set for physical isolation;
// wholecard adds gpu-spec, vGPU adds gpu-sharing-spec + gpu-sharing-policy.
func buildNodeSelector(spec ports.GPUSpecCRD) map[string]string {
	selector := map[string]string{
		gpuModeLabelKey: spec.NodeAffinity.GPUMode,
	}
	if spec.GPUMode == "wholecard" {
		selector[gpuSpecLabelKey] = spec.NodeAffinity.GPUSpec
	} else {
		selector[gpuSharingSpecLabelKey] = spec.NodeAffinity.GPUSharingSpec
		selector[gpuSharingPolicyLabelKey] = spec.NodeAffinity.GPUSharingPolicy
	}
	return selector
}

// volcanoVGPUResourceName is the Volcano vGPU memory resource key.
// Volcano's vGPU memory allocation = volcano.sh/vgpu-memory * volcanoVGPUFactor,
// so the value written to the Pod must be mb_per_share / volcanoVGPUFactor.
const volcanoVGPUResourceName = "volcano.sh/vgpu-memory"

// volcanoVGPUFactor is the multiplier Volcano applies to volcano.sh/vgpu-memory
// to compute the actual allocated MB. The value 10 means a Pod requesting
// volcano.sh/vgpu-memory=1228 gets 12280 MB of GPU memory.
const volcanoVGPUFactor = 10

// buildResourceRequests formats the VolcanoResources map entries with the
// requested count and (for vGPU) the spec's MBPerShare. Placeholders {count}
// and {mb_per_share} in the map values are replaced with concrete numbers.
// For vGPU, volcano.sh/vgpu-memory must never resolve to an empty string.
func buildResourceRequests(spec ports.GPUSpecCRD, count int) map[string]string {
	requests := make(map[string]string)
	countStr := strconv.Itoa(count)
	mbStr := strconv.Itoa(spec.MBPerShare)
	if spec.GPUMode == "wholecard" {
		for k, v := range spec.VolcanoResources.Wholecard {
			requests[k] = formatResourceValue(v, countStr, mbStr)
		}
	} else {
		// Volcano allocates volcano.sh/vgpu-memory * factor MB, so divide
		// mb_per_share by the factor to get the Pod resource value.
		// Use floor division: each slice must not exceed its share of
		// the GPU memory, otherwise the last slice would request more
		// than the remaining GPU memory and fail to schedule.
		// mb_per_share >= 10 is enforced at creation time (gpu_spec_resources.go).
		vgpuMemStr := strconv.Itoa(spec.MBPerShare / volcanoVGPUFactor)
		for k, v := range spec.VolcanoResources.VGPU {
			var formatted string
			if k == volcanoVGPUResourceName {
				formatted = formatResourceValue(v, countStr, vgpuMemStr)
			} else {
				formatted = formatResourceValue(v, countStr, mbStr)
			}
			// volcano.sh/vgpu-memory must never be empty (SPEC §5.1, FR-26).
			if k == volcanoVGPUResourceName && formatted == "" {
				formatted = vgpuMemStr
			}
			requests[k] = formatted
		}
	}
	return requests
}

// formatResourceValue replaces {count} and {mb_per_share} placeholders in
// the resource template value with the provided concrete strings.
func formatResourceValue(template, countStr, mbStr string) string {
	result := template
	result = strings.ReplaceAll(result, placeholderCount, countStr)
	result = strings.ReplaceAll(result, placeholderMBPerShare, mbStr)
	return result
}

// buildQueueAnnotation creates the Volcano queue annotation map. The
// queue_name is written into scheduling.volcano.sh/queue-name so the
// Volcano scheduler routes the Pod through the specified queue.
func buildQueueAnnotation(queueName string) map[string]string {
	return map[string]string{
		volcanoQueueAnnotationKey: queueName,
	}
}
