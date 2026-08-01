package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	sandboxCodeRunMaxOutputBytes = 64 * 1024
	sandboxPodReadyPollInterval  = 2 * time.Second
)

type sandboxPodExecRequest struct {
	Namespace string
	Pod       string
	Container string
	Command   []string
	Stdin     string
	Timeout   time.Duration
}

type sandboxPodExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type sandboxPodExecutor interface {
	Exec(ctx context.Context, request sandboxPodExecRequest) (sandboxPodExecResult, error)
}

type kubectlSandboxPodExecutor struct {
	binary string
	runner func(ctx context.Context, binary string, args []string, stdin string) (stdout string, stderr string, exitCode int, err error)
}

func newKubectlSandboxPodExecutor() *kubectlSandboxPodExecutor {
	return &kubectlSandboxPodExecutor{
		binary: "kubectl",
		runner: runKubectlExec,
	}
}

func (e *kubectlSandboxPodExecutor) Exec(ctx context.Context, request sandboxPodExecRequest) (sandboxPodExecResult, error) {
	if strings.TrimSpace(request.Namespace) == "" || strings.TrimSpace(request.Pod) == "" {
		return sandboxPodExecResult{}, fmt.Errorf("%w: namespace and pod are required", ports.ErrInvalid)
	}
	if len(request.Command) == 0 {
		return sandboxPodExecResult{}, fmt.Errorf("%w: command is required", ports.ErrInvalid)
	}
	args := []string{"exec", "-n", request.Namespace, request.Pod}
	if strings.TrimSpace(request.Container) != "" {
		args = append(args, "-c", request.Container)
	}
	if request.Stdin != "" {
		args = append(args, "-i")
	}
	args = append(args, "--")
	args = append(args, request.Command...)

	execCtx := ctx
	var cancel context.CancelFunc
	if request.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}
	stdout, stderr, exitCode, err := e.runner(execCtx, e.binary, args, request.Stdin)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && exitCode == 0 {
		return sandboxPodExecResult{}, err
	}
	return sandboxPodExecResult{Stdout: stdout, Stderr: stderr, ExitCode: exitCode}, err
}

func runKubectlExec(ctx context.Context, binary string, args []string, stdin string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
			err = nil
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout.String(), stderr.String(), -1, context.DeadlineExceeded
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

func (r *KubernetesSandboxRuntime) runCodeInPod(ctx context.Context, request ports.SandboxCodeRunRequest, instance ports.SandboxInstanceStatus) (ports.SandboxCodeRunResult, error) {
	now := firstNonZeroTime(request.RequestedAt, r.now().UTC())
	result := ports.SandboxCodeRunResult{
		Language:  request.Language,
		CreatedAt: now,
	}
	if !r.enabled || r.client == nil || r.executor == nil {
		result.Status = "accepted"
		return result, nil
	}

	podName, containerName, err := r.waitReadySandboxPod(ctx, instance, time.Duration(request.TimeoutSeconds)*time.Second)
	if err != nil {
		return ports.SandboxCodeRunResult{}, err
	}
	command, err := sandboxCodeRunCommand(request.Language, request.Code)
	if err != nil {
		return ports.SandboxCodeRunResult{}, err
	}

	execResult, execErr := r.executor.Exec(ctx, sandboxPodExecRequest{
		Namespace: tenantNamespace(instance.TenantID),
		Pod:       podName,
		Container: containerName,
		Command:   command,
		Stdin:     request.Stdin,
		Timeout:   time.Duration(request.TimeoutSeconds) * time.Second,
	})
	stdout, truncatedStdout := truncateSandboxOutput(execResult.Stdout, sandboxCodeRunMaxOutputBytes)
	stderr, truncatedStderr := truncateSandboxOutput(execResult.Stderr, sandboxCodeRunMaxOutputBytes)
	completed := r.now().UTC()
	result.Stdout = stdout
	result.Stderr = stderr
	result.Truncated = truncatedStdout || truncatedStderr
	result.CompletedAt = &completed
	exitCode := execResult.ExitCode
	result.ExitCode = &exitCode

	switch {
	case errors.Is(execErr, context.DeadlineExceeded):
		result.Status = "timed_out"
	case execErr != nil:
		return ports.SandboxCodeRunResult{}, execErr
	case exitCode != 0:
		result.Status = "failed"
	default:
		result.Status = "succeeded"
	}
	return result, nil
}

func sandboxCodeRunCommand(language string, code string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "python":
		return []string{"python3", "-c", code}, nil
	case "javascript":
		return []string{"node", "-e", code}, nil
	default:
		return nil, fmt.Errorf("%w: language must be python or javascript", ports.ErrInvalid)
	}
}

func truncateSandboxOutput(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return value[:limit], true
}

func (r *KubernetesSandboxRuntime) waitReadySandboxPod(ctx context.Context, instance ports.SandboxInstanceStatus, budget time.Duration) (string, string, error) {
	if budget <= 0 {
		budget = 60 * time.Second
	}
	// Wall clock for polling so unit tests with frozen r.now() cannot spin forever.
	deadline := time.Now().Add(budget)
	var lastErr error
	for {
		podName, containerName, err := r.findReadySandboxPod(ctx, instance)
		if err == nil {
			return podName, containerName, nil
		}
		lastErr = err
		if !errors.Is(err, ports.ErrFailedPrecondition) {
			return "", "", err
		}
		if !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(sandboxPodReadyPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", "", ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: sandbox pod is not ready", ports.ErrFailedPrecondition)
	}
	return "", "", lastErr
}

func (r *KubernetesSandboxRuntime) findReadySandboxPod(ctx context.Context, instance ports.SandboxInstanceStatus) (string, string, error) {
	namespace := tenantNamespace(instance.TenantID)
	selector := url.Values{}
	selector.Set("labelSelector", fmt.Sprintf(
		"ani.kubercloud.io/tenant-id=%s,ani.kubercloud.io/instance=%s",
		instance.TenantID,
		instance.Name,
	))
	endpoint := r.client.host + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods?" + selector.Encode()
	body, err := r.client.do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return "", "", err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Name  string `json:"name"`
					Ready bool   `json:"ready"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", "", fmt.Errorf("%w: decode sandbox pod list: %v", ports.ErrInvalid, err)
	}
	for _, item := range list.Items {
		if !strings.EqualFold(item.Status.Phase, "Running") {
			continue
		}
		container := firstNonEmpty(instance.Name, item.Metadata.Name)
		ready := false
		for _, status := range item.Status.ContainerStatuses {
			if status.Ready && (status.Name == container || status.Name == instance.Name || len(item.Status.ContainerStatuses) == 1) {
				container = firstNonEmpty(status.Name, container)
				ready = true
				break
			}
		}
		if ready {
			return item.Metadata.Name, container, nil
		}
	}
	return "", "", fmt.Errorf("%w: sandbox pod is not ready", ports.ErrFailedPrecondition)
}
