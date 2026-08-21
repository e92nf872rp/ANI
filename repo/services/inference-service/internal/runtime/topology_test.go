package runtime

import (
	"errors"
	"testing"

	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

func TestPlanTopologyCPUAndGPUPlacement(t *testing.T) {
	caps := CapabilityView{
		SupportedTopologyModes: []string{"single_node", "leader_worker"},
		LeaderWorkerSetReady:   true,
		GangSchedulingReady:    true,
		AcceleratorSpecs: []AcceleratorView{{
			SpecID: "gpu-a100-full", Available: true, MaxSingleNodeCount: 1,
		}},
	}
	cpu, err := PlanTopology(domain.Spec{Replicas: 1, PlacementMode: "auto"}, CapabilityView{})
	if err != nil || cpu.Mode != "single_node" || cpu.Gang {
		t.Fatalf("cpu plan = %+v, %v", cpu, err)
	}
	gpu, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "auto",
		Accelerator: &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 1},
	}, caps)
	if err != nil || gpu.Mode != "single_node" {
		t.Fatalf("single gpu plan = %+v, %v", gpu, err)
	}
	lws, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "multi_node",
		Accelerator: &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
	}, caps)
	if err != nil || lws.Mode != "leader_worker" || !lws.Gang || lws.WorkerCount != 1 {
		t.Fatalf("lws plan = %+v, %v", lws, err)
	}
	autoLWS, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "auto",
		Accelerator: &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
	}, caps)
	if err != nil || autoLWS.Mode != "leader_worker" {
		t.Fatalf("auto lws plan = %+v, %v", autoLWS, err)
	}
	if _, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "multi_node",
		Accelerator: &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
	}, CapabilityView{AcceleratorSpecs: caps.AcceleratorSpecs}); !errors.Is(err, ErrUnsupportedTopology) {
		t.Fatalf("lws without ready CRDs error = %v", err)
	}
	if _, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "single_node",
		Accelerator: &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
	}, caps); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("oversize single-node error = %v", err)
	}
	if _, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "auto",
		Accelerator: &domain.Accelerator{SpecID: "missing", CountPerReplica: 1},
	}, caps); !errors.Is(err, ErrRuntimeUnsupported) {
		t.Fatalf("missing spec error = %v", err)
	}
	if _, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "multi_node",
		Accelerator:      &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
		ExecutionProfile: domain.ExecutionProfile{Runtime: "sglang"},
	}, caps); !errors.Is(err, ErrUnsupportedTopology) {
		t.Fatalf("sglang lws error = %v", err)
	}
	if _, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "multi_node",
		Accelerator: &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
		Engine:      &domain.Engine{Command: []string{"python3", "-m", "vllm.entrypoints.openai.api_server"}},
	}, caps); !errors.Is(err, ErrUnsupportedTopology) {
		t.Fatalf("tenant command without ray error = %v", err)
	}
	rayOK, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "multi_node",
		Accelerator: &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
		Engine:      &domain.Engine{Command: []string{"sh", "-c", "ray start --head && python3 -m vllm.entrypoints.openai.api_server"}},
	}, caps)
	if err != nil || rayOK.Mode != "leader_worker" {
		t.Fatalf("tenant ray command plan = %+v, %v", rayOK, err)
	}
}

func TestPlanTopologyAcceptsLegacyAcceleratorSuffix(t *testing.T) {
	caps := CapabilityView{
		SupportedTopologyModes: []string{"single_node"},
		AcceleratorSpecs: []AcceleratorView{{
			SpecID: "gpu-nvidia-geforce-rtx-4090", Available: true, MaxSingleNodeCount: 2,
		}},
	}
	got, err := PlanTopology(domain.Spec{
		Replicas: 1, PlacementMode: "auto",
		Accelerator: &domain.Accelerator{SpecID: "gpu-nvidia-geforce-rtx-4090-full", CountPerReplica: 1},
	}, caps)
	if err != nil || got.Mode != "single_node" {
		t.Fatalf("legacy suffix plan = %+v, %v", got, err)
	}
}
