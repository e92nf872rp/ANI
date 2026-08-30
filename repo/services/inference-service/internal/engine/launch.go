package engine

import (
	"strconv"
	"strings"

	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

const (
	defaultListenPort = "8000"
	defaultModelPath  = "/models"
	runtimeVLLM       = "vllm"
	runtimeSGLang     = "sglang"
)

// Artifact 把 catalog storage_path 拆成 Core object_ref 和容器内 --model 路径。
// 租户本地模型：pvc://<claim>#/models/<subdir>。Core 整盘挂到 /models，# 后不是 K8s subPath。
func Artifact(ref string) (objectRef, modelPath string) {
	objectRef = strings.TrimSpace(ref)
	modelPath = defaultModelPath
	if objectRef == "" {
		return "", modelPath
	}
	if head, tail, found := strings.Cut(objectRef, "#"); found {
		objectRef = strings.TrimSpace(head)
		if strings.TrimSpace(tail) != "" {
			modelPath = strings.TrimSpace(tail)
		}
	}
	return objectRef, modelPath
}

// Launch 按冻结的 ExecutionProfile.Runtime 拼容器 command/args。Core 不认识 vLLM/SGLang。
// 租户 engine.command 是完整 argv，原样作为容器启动命令，不拼接、不追加平台默认 command。
func Launch(spec domain.Spec, servedModelName string) (command []string, args []string) {
	if command, args, ok := frozenLaunch(spec); ok {
		return command, args
	}
	switch engineRuntime(spec) {
	case runtimeSGLang:
		return launchSGLang(spec, servedModelName)
	default:
		return launchVLLM(spec, servedModelName)
	}
}

func frozenLaunch(spec domain.Spec) (command []string, args []string, ok bool) {
	if spec.Engine == nil || len(spec.Engine.Command) == 0 {
		return nil, nil, false
	}
	return append([]string(nil), spec.Engine.Command...), nil, true
}

func engineRuntime(spec domain.Spec) string {
	runtime := strings.ToLower(strings.TrimSpace(spec.ExecutionProfile.Runtime))
	if runtime == "" {
		return runtimeVLLM
	}
	return runtime
}

func launchVLLM(spec domain.Spec, servedModelName string) (command []string, args []string) {
	_, modelPath := Artifact(spec.ExecutionProfile.ArtifactRef)
	name := servedModelNameOrDefault(servedModelName)
	server := []string{
		"python3", "-m", "vllm.entrypoints.openai.api_server",
		"--model", modelPath,
		"--served-model-name", name,
		"--host", "0.0.0.0",
		"--port", defaultListenPort,
	}
	if domain.NormalizeInferenceTask(spec.ExecutionProfile.Task) == domain.InferenceTaskEmbed {
		server = append(server, "--runner", "pooling", "--convert", "embed")
	}
	if spec.Accelerator == nil {
		command = []string{"env"}
		args = append([]string{
			"VLLM_CPU_KVCACHE_SPACE=2",
			"OMP_NUM_THREADS=4",
			"HF_HOME=/tmp/hf",
		}, server...)
		args = append(args, "--dtype", "float32", "--max-model-len", "1024", "--max-num-seqs", "1", "--enforce-eager")
		return command, args
	}
	if spec.Accelerator.CountPerReplica > 1 {
		server = append(server, "--tensor-parallel-size", strconv.Itoa(spec.Accelerator.CountPerReplica))
		server = append(server, "--disable-custom-all-reduce")
	}
	// GPU V1 默认 torch.compile + cudagraph 会让 /health 卡住数十分钟；P0 与 CPU 一样关 compile。
	server = append(server, "--enforce-eager")
	return server[:3], server[3:]
}

func launchSGLang(spec domain.Spec, servedModelName string) (command []string, args []string) {
	_, modelPath := Artifact(spec.ExecutionProfile.ArtifactRef)
	name := servedModelNameOrDefault(servedModelName)
	server := []string{
		"python3", "-m", "sglang.launch_server",
		"--model-path", modelPath,
		"--served-model-name", name,
		"--host", "0.0.0.0",
		"--port", defaultListenPort,
	}
	if spec.Accelerator == nil {
		server = append(server, "--device", "cpu")
		return server[:3], server[3:]
	}
	if spec.Accelerator.CountPerReplica > 1 {
		server = append(server, "--tp-size", strconv.Itoa(spec.Accelerator.CountPerReplica))
	}
	return server[:3], server[3:]
}

// LaunchLeader 是 LWS leader 入口。每个 leader Pod 1 张卡，Ray 看成独立 node。
func LaunchLeader(spec domain.Spec, servedModelName string) (command []string, args []string) {
	if command, args, ok := frozenLaunch(spec); ok {
		return command, args
	}
	if engineRuntime(spec) == runtimeSGLang {
		return Launch(spec, servedModelName)
	}
	_, serverArgs := Launch(spec, servedModelName)
	// vLLM 会按 Ray 集群 GPU 编号覆盖 CUDA_VISIBLE_DEVICES；每 Pod 只有 index 0。
	writeSite := `python3 -c 'open("/tmp/sitecustomize.py","w").write("import os\n_s=os.environ.__class__.__setitem__\ndef _g(e,k,v):\n    if k==\"CUDA_VISIBLE_DEVICES\": v=\"0\"\n    if k==\"NVIDIA_VISIBLE_DEVICES\": v=\"all\"\n    _s(e,k,v)\nos.environ.__class__.__setitem__=_g\nos.environ[\"NVIDIA_VISIBLE_DEVICES\"]=\"all\"\nos.environ[\"CUDA_VISIBLE_DEVICES\"]=\"0\"\n")'`
	rayEnv := "env NVIDIA_VISIBLE_DEVICES=all CUDA_VISIBLE_DEVICES=0 PYTHONPATH=/tmp RAY_EXPERIMENTAL_NOSET_CUDA_VISIBLE_DEVICES=1 VLLM_USE_RAY_COMPILED_DAG=0"
	parts := []string{
		writeSite + " && " + rayEnv + " ray start --head --port=6379 --num-gpus=1 --disable-usage-stats && " + rayEnv + " python3 -m vllm.entrypoints.openai.api_server",
	}
	parts = append(parts, serverArgs...)
	if spec.Accelerator != nil && spec.Accelerator.CountPerReplica > 0 && !containsLaunchArg(serverArgs, "--distributed-executor-backend") {
		parts = append(parts, "--distributed-executor-backend", "ray")
	}
	return []string{"sh", "-c"}, []string{strings.Join(parts, " ")}
}

func servedModelNameOrDefault(servedModelName string) string {
	name := strings.TrimSpace(servedModelName)
	if name == "" {
		return "default"
	}
	return name
}

func containsLaunchArg(args []string, key string) bool {
	for _, item := range args {
		if item == key {
			return true
		}
	}
	return false
}
