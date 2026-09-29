package runtime

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	platformWorkloadLWSCRD      = "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/leaderworkersets.leaderworkerset.x-k8s.io"
	platformWorkloadPodGroupCRD = "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/podgroups.scheduling.volcano.sh"
)

func (r *KubernetesPlatformWorkloadRuntime) DiscoverCapabilities(ctx context.Context) (ports.PlatformWorkloadCapabilities, error) {
	caps := defaultPlatformWorkloadCapabilities()
	if r == nil || r.client == nil {
		return caps, nil
	}
	lwsReady := r.clusterResourceExists(ctx, platformWorkloadLWSCRD)
	volcanoReady := r.clusterResourceExists(ctx, platformWorkloadPodGroupCRD)
	caps.LeaderWorkerSetReady = lwsReady
	caps.GangSchedulingReady = volcanoReady
	if lwsReady && volcanoReady {
		caps.SupportedTopologyModes = []string{"single_node", "leader_worker"}
		caps.SupportedProfiles = append(caps.SupportedProfiles, ports.PlatformWorkloadTopologyProfile{
			ID: "container-leader-worker", Version: "v1", Mode: "leader_worker",
		})
	}
	nodes, err := NewKubernetesGPUInventory(r.client).ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		return caps, nil
	}
	caps.AcceleratorSpecs = acceleratorSpecsFromGPUNodes(nodes, volcanoReady)
	if r.specStore != nil {
		specs, specErr := r.specStore.List(ctx)
		if specErr != nil {
			return caps, specErr
		}
		caps.AcceleratorSpecs = mergeGPUSpecCapabilities(caps.AcceleratorSpecs, specs)
	}
	return caps, nil
}

func mergeGPUSpecCapabilities(base []ports.PlatformWorkloadAcceleratorCapability, specs []ports.GPUSpecCRD) []ports.PlatformWorkloadAcceleratorCapability {
	out := make([]ports.PlatformWorkloadAcceleratorCapability, 0, len(base)+len(specs))
	for _, spec := range specs {
		id := strings.TrimSpace(spec.ID)
		if id == "" {
			continue
		}
		memoryPerShare := 0
		if strings.TrimSpace(spec.GPUMode) == "vgpu" {
			memoryPerShare = spec.MBPerShare
		}
		item := ports.PlatformWorkloadAcceleratorCapability{
			SpecID:             id,
			Available:          false,
			GPUMode:            strings.TrimSpace(spec.GPUMode),
			MemoryPerShareMB:   memoryPerShare,
			MaxSingleNodeCount: 0,
		}
		for index := range base {
			model := &base[index]
			if !acceleratorModelMatches(spec, model) {
				continue
			}
			item.Available = spec.Available && model.Available
			if item.GPUMode == "wholecard" {
				item.MaxWholeCardCount = model.MaxWholeCardCount
				item.MaxSingleNodeCount = model.MaxWholeCardCount
			} else {
				item.MaxVGPUCount = model.MaxVGPUCount
				item.MaxSingleNodeCount = model.MaxVGPUCount
			}
			item.Aliases = append(item.Aliases, model.SpecID)
			item.Aliases = append(item.Aliases, model.Aliases...)
			break
		}
		out = append(out, item)
	}
	return out
}

func acceleratorModelMatches(spec ports.GPUSpecCRD, capability *ports.PlatformWorkloadAcceleratorCapability) bool {
	if capability == nil {
		return false
	}
	if spec.GPUMode == "wholecard" && capability.MaxWholeCardCount < 1 {
		return false
	}
	if spec.GPUMode == "vgpu" && capability.MaxVGPUCount < 1 {
		return false
	}
	for _, value := range []string{spec.GPUType, spec.NodeAffinity.GPUSpec, spec.NodeAffinity.GPUSharingSpec} {
		if acceleratorModelKey(value) != "" && strings.Contains(acceleratorModelKey(capability.SpecID), acceleratorModelKey(value)) {
			return true
		}
	}
	return false
}

func acceleratorModelKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = regexp.MustCompile(`[-]?\d+(mib|gb)$`).ReplaceAllString(value, "")
	value = strings.NewReplacer("gpu-", "", "nvidia-", "", "geforce-", "", "rtx-", "", "-", "", "_", "", " ", "").Replace(value)
	return value
}

func (r *KubernetesPlatformWorkloadRuntime) clusterResourceExists(ctx context.Context, path string) bool {
	_, status, err := r.client.Do(ctx, http.MethodGet, strings.TrimRight(r.client.host, "/")+path, "", nil)
	return err == nil && status == http.StatusOK
}

// acceleratorSpecsFromGPUNodes 按 GPU 型号聚合广告。SpecID 只是型号，同一型号的整卡节点和 vGPU 节点合并成一条。
// 对外仍只填 MaxSingleNodeCount 作为提示；准入必须用 MaxWholeCardCount / MaxVGPUCount。
func acceleratorSpecsFromGPUNodes(nodes []ports.GPUNodeClass, volcanoReady bool) []ports.PlatformWorkloadAcceleratorCapability {
	type agg struct {
		wholeMax int
		vgpuMax  int
		ready    bool
	}
	byID := map[string]*agg{}
	add := func(id string, wholeCount, vgpuCount int, ready bool) {
		if strings.TrimSpace(id) == "" {
			return
		}
		item := byID[id]
		if item == nil {
			item = &agg{}
			byID[id] = item
		}
		if wholeCount > item.wholeMax {
			item.wholeMax = wholeCount
		}
		if vgpuCount > item.vgpuMax {
			item.vgpuMax = vgpuCount
		}
		item.ready = item.ready || (ready && (wholeCount > 0 || vgpuCount > 0))
	}
	for _, node := range nodes {
		gpuType := strings.TrimSpace(firstNonEmpty(node.Model, "nvidia"))
		for _, device := range node.Devices {
			if model := strings.TrimSpace(device.Model); model != "" {
				gpuType = model
				if device.VirtualizationMode == ports.GPUVirtualizationNone {
					break
				}
			}
		}
		if gpuType == "" {
			gpuType = "nvidia"
		}
		modelID := gpuModelSpecID(gpuType)
		wholeCount := gpuAllocatableCount(node, kubernetesNVIDIAGPUResource)
		volcanoCount := gpuAllocatableCount(node, kubernetesVolcanoVGPUNumberResource)
		if wholeCount < 1 && volcanoCount < 1 {
			continue
		}
		add(modelID, wholeCount, volcanoCount, node.Ready)
	}
	out := make([]ports.PlatformWorkloadAcceleratorCapability, 0, len(byID))
	for id, item := range byID {
		maxCount := item.wholeMax
		if item.vgpuMax > maxCount {
			maxCount = item.vgpuMax
		}
		out = append(out, ports.PlatformWorkloadAcceleratorCapability{
			SpecID:             id,
			Available:          volcanoReady && item.ready && maxCount > 0,
			MaxSingleNodeCount: maxCount,
			MaxWholeCardCount:  item.wholeMax,
			MaxVGPUCount:       item.vgpuMax,
		})
	}
	return out
}
