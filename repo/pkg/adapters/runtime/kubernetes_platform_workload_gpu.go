package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// ResolveAccelerator freezes the catalog-derived scheduling fragments in the
// workload intent. Retries can therefore render the same node selector and
// Volcano resources even if the mutable GPUSpec catalog changes later.
func (r *KubernetesPlatformWorkloadRuntime) ResolveAccelerator(ctx context.Context, spec *ports.PlatformWorkloadCreateSpec) error {
	if spec == nil || spec.Resources.AcceleratorSpecID == "" || spec.Resources.AcceleratorCount < 1 {
		return nil
	}
	if len(spec.Resources.AcceleratorNodeSelector) > 0 && len(spec.Resources.AcceleratorResourceRequests) > 0 {
		return nil
	}
	if r == nil || r.specStore == nil {
		return nil
	}
	definition, err := r.specStore.Get(ctx, strings.TrimSpace(spec.Resources.AcceleratorSpecID))
	if errors.Is(err, ports.ErrGPUSpecNotFound) {
		definition, err = r.resolveLegacyAccelerator(ctx, spec.Resources.AcceleratorSpecID, spec.Resources.AcceleratorMemoryMB)
	}
	if err != nil {
		return err
	}
	if !definition.Available {
		return fmt.Errorf("%w: gpu spec %q is disabled", ports.ErrFailedPrecondition, definition.ID)
	}
	if definition.GPUMode == "wholecard" && spec.Resources.AcceleratorMemoryMB != 0 {
		return fmt.Errorf("%w: wholecard GPU spec %q does not accept memory", ports.ErrInvalid, definition.ID)
	}
	if definition.GPUMode == "vgpu" && (definition.MBPerShare < 1 || spec.Resources.AcceleratorMemoryMB != definition.MBPerShare) {
		return fmt.Errorf("%w: vGPU spec %q requires memory=%d MiB", ports.ErrInvalid, definition.ID, definition.MBPerShare)
	}
	// Persist the catalog ID after resolving a migration alias. The scheduling
	// snapshot and subsequent labels must never depend on the old model key.
	spec.Resources.AcceleratorSpecID = definition.ID
	selector := buildNodeSelector(definition)
	for key, value := range selector {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			delete(selector, key)
		}
	}
	if len(selector) == 0 {
		return fmt.Errorf("%w: gpu spec %q has no node affinity", ports.ErrFailedPrecondition, definition.ID)
	}
	spec.Resources.AcceleratorNodeSelector = selector
	spec.Resources.AcceleratorResourceRequests = buildResourceRequests(definition, spec.Resources.AcceleratorCount)
	spec.Resources.AcceleratorSchedulerName = volcanoSchedulerName
	return nil
}

func (r *KubernetesPlatformWorkloadRuntime) resolveLegacyAccelerator(ctx context.Context, requested string, memoryMB int) (ports.GPUSpecCRD, error) {
	specs, err := r.specStore.List(ctx)
	if err != nil {
		return ports.GPUSpecCRD{}, err
	}
	want := strings.TrimSpace(requested)
	legacy := strings.HasPrefix(strings.ToLower(want), "gpu-")
	if !legacy {
		return ports.GPUSpecCRD{}, fmt.Errorf("%w: gpu spec alias %q is not a supported legacy model ID", ports.ErrGPUSpecNotFound, requested)
	}
	var matches []ports.GPUSpecCRD
	modelKey := acceleratorModelKey(want)
	for _, candidate := range specs {
		if !candidate.Available || modelKey == "" || !candidateMatchesModelKey(candidate, modelKey) {
			continue
		}
		if candidate.GPUMode == "vgpu" && memoryMB != candidate.MBPerShare {
			continue
		}
		if candidate.GPUMode == "wholecard" && memoryMB != 0 {
			continue
		}
		matches = append(matches, candidate)
	}
	if len(matches) != 1 {
		return ports.GPUSpecCRD{}, fmt.Errorf("%w: gpu spec alias %q is ambiguous or unavailable", ports.ErrFailedPrecondition, requested)
	}
	return matches[0], nil
}

func candidateMatchesModelKey(candidate ports.GPUSpecCRD, want string) bool {
	for _, value := range []string{candidate.GPUType, candidate.NodeAffinity.GPUSpec, candidate.NodeAffinity.GPUSharingSpec} {
		if key := acceleratorModelKey(value); key != "" && (key == want || strings.Contains(key, want) || strings.Contains(want, key)) {
			return true
		}
	}
	return false
}
