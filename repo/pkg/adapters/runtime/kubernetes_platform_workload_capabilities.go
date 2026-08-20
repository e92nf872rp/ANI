package runtime

import (
	"context"
	"net/http"
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
	return caps, nil
}

func (r *KubernetesPlatformWorkloadRuntime) clusterResourceExists(ctx context.Context, path string) bool {
	_, status, err := r.client.Do(ctx, http.MethodGet, strings.TrimRight(r.client.host, "/")+path, "", nil)
	return err == nil && status == http.StatusOK
}

func acceleratorSpecsFromGPUNodes(nodes []ports.GPUNodeClass, volcanoReady bool) []ports.PlatformWorkloadAcceleratorCapability {
	type agg struct {
		maxCount    int
		ready       bool
		memoryShare int
	}
	byID := map[string]*agg{}
	add := func(id string, count, memoryShare int, ready bool) {
		if strings.TrimSpace(id) == "" {
			return
		}
		item := byID[id]
		if item == nil {
			item = &agg{}
			byID[id] = item
		}
		if count > item.maxCount {
			item.maxCount = count
		}
		if memoryShare > item.memoryShare {
			item.memoryShare = memoryShare
		}
		item.ready = item.ready || (ready && count > 0)
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
		wholeCount := gpuAllocatableCount(node, kubernetesNVIDIAGPUResource)
		if wholeCount > 0 {
			add(gpuSpecID(gpuType, 1), wholeCount, 0, node.Ready)
		}
		volcanoCount := gpuAllocatableCount(node, kubernetesVolcanoVGPUNumberResource)
		if volcanoCount < 1 {
			continue
		}
		memoryShare := 0
		if total := gpuAllocatableCount(node, kubernetesVolcanoVGPUMemoryResource); volcanoCount > 0 && total > 0 {
			memoryShare = total / volcanoCount
		}
		add(gpuSpecID(gpuType, volcanoVGPUShareCount(volcanoCount)), volcanoCount, memoryShare, node.Ready)
	}
	out := make([]ports.PlatformWorkloadAcceleratorCapability, 0, len(byID))
	for id, item := range byID {
		out = append(out, ports.PlatformWorkloadAcceleratorCapability{
			SpecID:             id,
			Available:          volcanoReady && item.ready && item.maxCount > 0,
			MaxSingleNodeCount: item.maxCount,
			MemoryPerShareMB:   item.memoryShare,
		})
	}
	return out
}

func volcanoVGPUShareCount(number int) int {
	switch number {
	case 2, 4, 8:
		return number
	default:
		return 4
	}
}
