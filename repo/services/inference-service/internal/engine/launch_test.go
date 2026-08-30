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
	if containsArg(cpuArgs, "--disable-custom-all-reduce") || !containsArg(gpuArgs, "--disable-custom-all-reduce") {
		t.Fatalf("custom all-reduce args cpu=%v gpu=%v", cpuArgs, gpuArgs)
	}
	if !containsArg(cpuArgs, "--enforce-eager") || !containsArg(gpuArgs, "--enforce-eager") {
		t.Fatalf("enforce-eager cpu=%v gpu=%v", cpuArgs, gpuArgs)
	}
}

func TestLaunchVLLMEmbeddingUsesPoolingEmbedCLI(t *testing.T) {
	tests := []struct {
		name        string
		accelerator *domain.Accelerator
	}{
		{name: "cpu"},
		{name: "gpu", accelerator: &domain.Accelerator{SpecID: "gpu-a100", CountPerReplica: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, args := Launch(domain.Spec{
				CPU: "4", Memory: "16Gi", Accelerator: tt.accelerator,
				ExecutionProfile: domain.ExecutionProfile{
					Runtime:     "vllm",
					Task:        domain.InferenceTaskEmbed,
					ArtifactRef: "pvc://vllm-model#/models/bge-m3",
				},
			}, "bge-m3")
			if !containsPair(args, "--runner", "pooling") || !containsPair(args, "--convert", "embed") {
				t.Fatalf("embedding args = %#v", args)
			}
			if containsArg(args, "--task") {
				t.Fatalf("embedding args use removed vLLM --task flag: %#v", args)
			}
		})
	}
}

func TestLaunchVLLMGenerateDoesNotAddTaskOverride(t *testing.T) {
	tests := []struct {
		name string
		task domain.InferenceTask
	}{
		{name: "generate", task: domain.InferenceTaskGenerate},
		{name: "legacy-empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, args := Launch(domain.Spec{ExecutionProfile: domain.ExecutionProfile{
				Runtime:     "vllm",
				Task:        tt.task,
				ArtifactRef: "pvc://vllm-model#/models/qwen",
			}}, "qwen")
			if containsArg(args, "--task") {
				t.Fatalf("generate args changed: %#v", args)
			}
		})
	}
}

func TestLaunchVLLMEmbeddingPreservesFrozenTenantCommand(t *testing.T) {
	want := []string{"custom-engine", "--model", "/models/bge-m3"}
	command, args := Launch(domain.Spec{
		Engine: &domain.Engine{Command: want},
		ExecutionProfile: domain.ExecutionProfile{
			Runtime: "vllm",
			Task:    domain.InferenceTaskEmbed,
		},
	}, "bge-m3")
	if strings.Join(command, "\x00") != strings.Join(want, "\x00") || len(args) != 0 {
		t.Fatalf("tenant command = %#v args=%#v", command, args)
	}
}

func TestLaunchLeaderVLLMEmbeddingUsesPoolingEmbedCLI(t *testing.T) {
	_, args := LaunchLeader(domain.Spec{
		PlacementMode: "multi_node",
		Accelerator:   &domain.Accelerator{SpecID: "gpu-a100", CountPerReplica: 2},
		ExecutionProfile: domain.ExecutionProfile{
			Runtime:     "vllm",
			Task:        domain.InferenceTaskEmbed,
			ArtifactRef: "pvc://vllm-model#/models/bge-m3",
		},
	}, "bge-m3")
	if len(args) != 1 || !strings.Contains(args[0], "--runner pooling") || !strings.Contains(args[0], "--convert embed") || strings.Contains(args[0], "--task") {
		t.Fatalf("leader embedding args = %#v", args)
	}
}

func TestLaunchLeaderStartsRayHead(t *testing.T) {
	command, args := LaunchLeader(domain.Spec{
		CPU: "8", Memory: "32Gi", PlacementMode: "multi_node",
		Accelerator:      &domain.Accelerator{SpecID: "gpu-a100-full", CountPerReplica: 2},
		ExecutionProfile: domain.ExecutionProfile{ArtifactRef: "pvc://vllm-model#/models/qwen"},
	}, "tiny-gpu")
	if len(command) != 2 || command[0] != "sh" || len(args) != 1 {
		t.Fatalf("command = %#v args=%#v", command, args)
	}
	if !strings.Contains(args[0], "ray start --head") || !strings.Contains(args[0], "--distributed-executor-backend ray") || !strings.Contains(args[0], "--tensor-parallel-size 2") {
		t.Fatalf("leader args = %q", args[0])
	}
	if !strings.Contains(args[0], "--enforce-eager") {
		t.Fatalf("leader must disable torch.compile: %q", args[0])
	}
	if !strings.Contains(args[0], "--num-gpus=1") || !strings.Contains(args[0], "RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES=1") || !strings.Contains(args[0], "sitecustomize.py") || !strings.Contains(args[0], "PYTHONPATH=/tmp") {
		t.Fatalf("leader must pin per-pod CUDA for Ray: %q", args[0])
	}
	if !strings.Contains(args[0], "VLLM_USE_RAY_COMPILED_DAG=0") {
		t.Fatalf("leader must disable Ray compiled DAG: %q", args[0])
	}
	if !strings.Contains(args[0], "--disable-custom-all-reduce") {
		t.Fatalf("leader TP>1 must disable custom all-reduce: %q", args[0])
	}
}

func TestLaunchSGLangUsesModelPathAndCPUDevice(t *testing.T) {
	command, args := Launch(domain.Spec{
		CPU: "4", Memory: "16Gi",
		ExecutionProfile: domain.ExecutionProfile{
			Runtime:     "sglang",
			ArtifactRef: "pvc://vllm-model#/models/qwen",
		},
	}, "tiny-sglang")
	if strings.Join(command, " ") != "python3 -m sglang.launch_server" {
		t.Fatalf("command = %#v", command)
	}
	if !containsPair(args, "--model-path", "/models/qwen") || !containsPair(args, "--device", "cpu") {
		t.Fatalf("args = %#v", args)
	}
	if containsPair(args, "--model", "/models/qwen") {
		t.Fatalf("sglang must not use vLLM --model: %#v", args)
	}
}

func TestLaunchUsesFrozenTenantCommandAsCompleteArgv(t *testing.T) {
	command, args := Launch(domain.Spec{
		CPU: "8", Memory: "32Gi",
		Accelerator: &domain.Accelerator{SpecID: "gpu-nvidia-geforce-rtx-4090-full", CountPerReplica: 1},
		Engine: &domain.Engine{Command: []string{
			"python3", "-m", "vllm.entrypoints.openai.api_server",
			"--model", "/models/qwen", "--host", "0.0.0.0", "--port", "8000",
		}},
		ExecutionProfile: domain.ExecutionProfile{ArtifactRef: "pvc://vllm-model#/models/qwen"},
	}, "tiny-gpu")
	want := []string{"python3", "-m", "vllm.entrypoints.openai.api_server", "--model", "/models/qwen", "--host", "0.0.0.0", "--port", "8000"}
	if strings.Join(command, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("command = %#v, want complete tenant argv", command)
	}
	if len(args) != 0 {
		t.Fatalf("args = %#v, want empty; tenant command is not appended to a platform default", args)
	}
}

func TestLaunchLeaderUsesFrozenTenantCommandWithoutRayWrap(t *testing.T) {
	command, args := LaunchLeader(domain.Spec{
		CPU: "8", Memory: "32Gi", PlacementMode: "multi_node",
		Accelerator:      &domain.Accelerator{SpecID: "gpu-nvidia-geforce-rtx-4090-full", CountPerReplica: 2},
		Engine:           &domain.Engine{Command: []string{"python3", "-m", "vllm.entrypoints.openai.api_server", "--model", "/models/qwen"}},
		ExecutionProfile: domain.ExecutionProfile{ArtifactRef: "pvc://vllm-model#/models/qwen"},
	}, "tiny-gpu")
	if len(command) != 5 || command[0] != "python3" || len(args) != 0 {
		t.Fatalf("leader tenant command = %#v args=%#v", command, args)
	}
	if strings.Join(command, " ") != "python3 -m vllm.entrypoints.openai.api_server --model /models/qwen" {
		t.Fatalf("leader must not wrap tenant command in sh -c: %#v", command)
	}
}

func TestArtifactSplitsPVCAndModelPath(t *testing.T) {
	objectRef, modelPath := Artifact("pvc://vllm-model#/models/qwen")
	if objectRef != "pvc://vllm-model" || modelPath != "/models/qwen" {
		t.Fatalf("artifact = %q %q", objectRef, modelPath)
	}
}

func containsArg(args []string, key string) bool {
	for _, item := range args {
		if item == key {
			return true
		}
	}
	return false
}

func containsPair(args []string, key, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == value {
			return true
		}
	}
	return false
}
