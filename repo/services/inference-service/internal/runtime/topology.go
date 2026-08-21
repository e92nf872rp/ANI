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
	// SpecID 是 GPU 型号。与创建请求的 spec_id 比对时剥掉历史 -full / -Nx。
	SpecID             string
	Available          bool
	MaxSingleNodeCount int
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
	item, ok := findAccelerator(caps, spec.Accelerator.SpecID)
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

func findAccelerator(caps CapabilityView, specID string) (AcceleratorView, bool) {
	want := canonicalAcceleratorSpecID(specID)
	for _, item := range caps.AcceleratorSpecs {
		if canonicalAcceleratorSpecID(item.SpecID) == want {
			return item, true
		}
	}
	return AcceleratorView{}, false
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
