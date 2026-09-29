package runtime

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

// CapabilityView 是 Core GET /platform-workload-capabilities 的投影。
type CapabilityView struct {
	SupportedTopologyModes []string
	LeaderWorkerSetReady   bool // 本集群 GPU/LWS 当前 skip
	GangSchedulingReady    bool
	AcceleratorSpecs       []AcceleratorView
}

type AcceleratorView struct {
	// SpecID is the public GPUSpec ID. Aliases are accepted only for
	// migration; exact public IDs always take precedence.
	SpecID             string
	Available          bool
	MaxSingleNodeCount int
	GPUMode            string
	MemoryPerShareMB   int
	Aliases            []string
}

// TopologyPlan 告诉 Core 用单节点 Deployment 还是 leader_worker。CPU 永远单节点。
type TopologyPlan struct {
	Mode           string
	ProfileID      string
	ProfileVersion string
	Gang           bool
	LeaderCount    int
	WorkerCount    int
	LeaderGPUs     int
	WorkerGPUs     int
}

// PlanTopology 按 placement_mode + GPU 能力选拓扑。CPU 多节点直接拒绝。
func PlanTopology(spec domain.Spec, caps CapabilityView) (TopologyPlan, error) {
	if spec.Accelerator == nil {
		if spec.PlacementMode == "multi_node" {
			return TopologyPlan{}, fmt.Errorf("%w: multi-node CPU inference is not supported", ErrUnsupportedTopology)
		}
		return TopologyPlan{Mode: "single_node", ProfileID: "container-single-node", ProfileVersion: "v1"}, nil
	}
	item, ok := findAccelerator(caps, spec.Accelerator.SpecID, spec.Accelerator.MemoryMB)
	if !ok || !item.Available {
		return TopologyPlan{}, ErrRuntimeUnsupported
	}
	switch spec.PlacementMode {
	case "single_node":
		if item.MaxSingleNodeCount < spec.Accelerator.CountPerReplica {
			return TopologyPlan{}, ErrInsufficientCapacity
		}
		return singleNodePlan(), nil
	case "multi_node":
		if spec.Replicas != 1 || spec.Accelerator.CountPerReplica < 2 {
			return TopologyPlan{}, fmt.Errorf("%w: multi-node inference requires replicas=1 and at least 2 GPUs", ErrUnsupportedTopology)
		}
		if !caps.LeaderWorkerSetReady || !caps.GangSchedulingReady || !supportsMode(caps, "leader_worker") {
			return TopologyPlan{}, fmt.Errorf("%w: leader_worker topology is not available", ErrUnsupportedTopology)
		}
		if err := admitLeaderWorker(spec); err != nil {
			return TopologyPlan{}, err
		}
		return leaderWorkerPlan(spec.Accelerator.CountPerReplica), nil
	default: // auto
		if item.MaxSingleNodeCount >= spec.Accelerator.CountPerReplica {
			return singleNodePlan(), nil
		}
		if spec.Replicas == 1 && spec.Accelerator.CountPerReplica >= 2 &&
			caps.LeaderWorkerSetReady && caps.GangSchedulingReady && supportsMode(caps, "leader_worker") {
			if err := admitLeaderWorker(spec); err != nil {
				return TopologyPlan{}, err
			}
			return leaderWorkerPlan(spec.Accelerator.CountPerReplica), nil
		}
		if item.MaxSingleNodeCount < spec.Accelerator.CountPerReplica {
			return TopologyPlan{}, ErrInsufficientCapacity
		}
		return TopologyPlan{}, fmt.Errorf("%w: no supported placement for accelerator request", ErrUnsupportedTopology)
	}
}

func singleNodePlan() TopologyPlan {
	return TopologyPlan{Mode: "single_node", ProfileID: "container-single-node", ProfileVersion: "v1"}
}

func leaderWorkerPlan(gpuCount int) TopologyPlan {
	return TopologyPlan{
		Mode: "leader_worker", ProfileID: "container-leader-worker", ProfileVersion: "v1",
		Gang: true, LeaderCount: 1, WorkerCount: gpuCount - 1, LeaderGPUs: 1, WorkerGPUs: 1,
	}
}

func admitLeaderWorker(spec domain.Spec) error {
	if strings.ToLower(strings.TrimSpace(spec.ExecutionProfile.Runtime)) == "sglang" {
		return fmt.Errorf("%w: sglang does not support leader_worker topology", ErrUnsupportedTopology)
	}
	if spec.Engine != nil && len(spec.Engine.Command) > 0 && !commandProvidesRay(spec.Engine.Command) {
		return fmt.Errorf("%w: multi-node inference requires a complete Ray leader command", ErrUnsupportedTopology)
	}
	return nil
}

func commandProvidesRay(command []string) bool {
	joined := strings.ToLower(strings.Join(command, " "))
	return strings.Contains(joined, "ray start") || strings.Contains(joined, "multi-node-serving.sh")
}

func findAccelerator(caps CapabilityView, specID string, memoryMB int) (AcceleratorView, bool) {
	want := strings.ToLower(strings.TrimSpace(specID))
	var aliases []AcceleratorView
	for _, item := range caps.AcceleratorSpecs {
		if strings.EqualFold(strings.TrimSpace(item.SpecID), want) && acceleratorViewMemoryMatches(item, memoryMB) {
			return item, true
		}
	}
	for _, item := range caps.AcceleratorSpecs {
		for _, alias := range item.Aliases {
			if strings.EqualFold(strings.TrimSpace(alias), want) && acceleratorViewMemoryMatches(item, memoryMB) {
				aliases = append(aliases, item)
				break
			}
		}
	}
	if len(aliases) == 1 {
		return aliases[0], true
	}
	if len(aliases) > 1 {
		return AcceleratorView{}, false
	}
	legacyWant := canonicalAcceleratorSpecID(want)
	for _, item := range caps.AcceleratorSpecs {
		if legacyWant != want && canonicalAcceleratorSpecID(item.SpecID) == legacyWant && acceleratorViewMemoryMatches(item, memoryMB) {
			return item, true
		}
	}
	return AcceleratorView{}, false
}

func acceleratorViewMemoryMatches(item AcceleratorView, memoryMB int) bool {
	switch item.GPUMode {
	case "vgpu":
		return memoryMB > 0 && item.MemoryPerShareMB > 0 && memoryMB == item.MemoryPerShareMB
	case "wholecard":
		return memoryMB == 0
	default:
		return true
	}
}

var legacyAcceleratorSuffix = regexp.MustCompile(`-\d+x$`)

// canonicalAcceleratorSpecID 把历史 -full / -Nx 剥掉，得到型号 ID。
func canonicalAcceleratorSpecID(specID string) string {
	id := strings.ToLower(strings.TrimSpace(specID))
	id = legacyAcceleratorSuffix.ReplaceAllString(id, "")
	return strings.TrimSuffix(id, "-full")
}

func supportsMode(caps CapabilityView, mode string) bool {
	for _, item := range caps.SupportedTopologyModes {
		if item == mode {
			return true
		}
	}
	return false
}
