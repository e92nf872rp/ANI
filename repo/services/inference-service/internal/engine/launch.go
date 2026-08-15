package engine

import (
	"strconv"
	"strings"

	"github.com/kubercloud/ani/services/inference-service/internal/domain"
)

const (
	defaultListenPort = "8000"
	defaultModelPath  = "/models"
)

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

func Launch(spec domain.Spec, servedModelName string) (command []string, args []string) {
	_, modelPath := Artifact(spec.ExecutionProfile.ArtifactRef)
	name := strings.TrimSpace(servedModelName)
	if name == "" {
		name = "default"
	}
	server := []string{
		"python3", "-m", "vllm.entrypoints.openai.api_server",
		"--model", modelPath,
		"--served-model-name", name,
		"--host", "0.0.0.0",
		"--port", defaultListenPort,
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
	}
	return server[:3], server[3:]
}
