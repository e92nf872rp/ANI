package engine

import (
	"strings"
	"testing"

	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

func TestLaunchUsesSameEntryForCPUAndGPU(t *testing.T) {
	cpuCommand, cpuArgs := Launch(domain.Spec{
		CPU: "4", Memory: "16Gi",
		ExecutionProfile: domain.ExecutionProfile{ArtifactRef: "pvc://vllm-model#/models/qwen"},
	}, "tiny-cpu")
	gpuCommand, gpuArgs := Launch(domain.Spec{
		CPU: "8", Memory: "32Gi",
		Accelerator:      &domain.Accelerator{SpecID: "gpu-a100", CountPerReplica: 2},
		ExecutionProfile: domain.ExecutionProfile{ArtifactRef: "pvc://vllm-model#/models/qwen"},
	}, "tiny-gpu")

	if !containsPair(cpuArgs, "--model", "/models/qwen") || !containsPair(gpuArgs, "--model", "/models/qwen") {
		t.Fatalf("model path cpu=%v gpu=%v", cpuArgs, gpuArgs)
	}
	if strings.Join(cpuCommand, " ") == strings.Join(gpuCommand, " ") {
		t.Fatal("CPU launch should wrap env; GPU uses the server entrypoint")
	}
	if !containsPair(cpuArgs, "--dtype", "float32") || containsPair(gpuArgs, "--dtype", "float32") {
		t.Fatalf("dtype args cpu=%v gpu=%v", cpuArgs, gpuArgs)
	}
	if containsPair(cpuArgs, "--tensor-parallel-size", "2") || !containsPair(gpuArgs, "--tensor-parallel-size", "2") {
		t.Fatalf("tp args cpu=%v gpu=%v", cpuArgs, gpuArgs)
	}
}

func TestArtifactSplitsPVCAndModelPath(t *testing.T) {
	objectRef, modelPath := Artifact("pvc://vllm-model#/models/qwen")
	if objectRef != "pvc://vllm-model" || modelPath != "/models/qwen" {
		t.Fatalf("artifact = %q %q", objectRef, modelPath)
	}
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
