package runtime

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesInstanceOps struct {
	client  *KubernetesRESTClient
	enabled bool
	now     func() time.Time
}

var kubeVirtSubresourceHeaders = map[string]string{
	"Accept": "*/*",
}

type KubernetesInstanceOpsOption func(*KubernetesInstanceOps)

func WithKubernetesInstanceOpsEnabled(enabled bool) KubernetesInstanceOpsOption {
	return func(ops *KubernetesInstanceOps) {
		ops.enabled = enabled
	}
}

func WithKubernetesInstanceOpsClock(now func() time.Time) KubernetesInstanceOpsOption {
	return func(ops *KubernetesInstanceOps) {
		if now != nil {
			ops.now = now
		}
	}
}

func NewKubernetesInstanceOps(client *KubernetesRESTClient, options ...KubernetesInstanceOpsOption) *KubernetesInstanceOps {
	ops := &KubernetesInstanceOps{client: client, now: time.Now}
	for _, option := range options {
		option(ops)
	}
	return ops
}

func (o *KubernetesInstanceOps) Run(ctx context.Context, request ports.WorkloadInstanceOpsRequest, record ports.WorkloadInstanceRecord) (ports.WorkloadInstanceOpsResult, error) {
	if err := validateOpsRequest(request, record); err != nil {
		return ports.WorkloadInstanceOpsResult{}, err
	}
	if !o.enabled {
		return ports.WorkloadInstanceOpsResult{
			Action:    request.Action,
			Accepted:  false,
			Reason:    "kubernetes instance ops are disabled by execution switch",
			CheckedAt: o.now().UTC(),
		}, nil
	}
	if o.client == nil {
		return ports.WorkloadInstanceOpsResult{}, ports.ErrNotConfigured
	}
	output, sessionID, err := o.execute(ctx, request, record)
	if err != nil {
		return ports.WorkloadInstanceOpsResult{}, err
	}
	connectURL := opsConnectURL(request, record, o.now().UTC())
	return ports.WorkloadInstanceOpsResult{
		Action:     request.Action,
		Accepted:   true,
		SessionID:  sessionID,
		Protocol:   opsProtocol(request),
		ConnectURL: connectURL,
		URL:        connectURL,
		Output:     output,
		Reason:     "accepted by Kubernetes instance ops",
		CheckedAt:  o.now().UTC(),
		ExpiresAt:  o.now().UTC().Add(15 * time.Minute),
	}, nil
}

func (o *KubernetesInstanceOps) execute(ctx context.Context, request ports.WorkloadInstanceOpsRequest, record ports.WorkloadInstanceRecord) (string, string, error) {
	namespace := tenantNamespace(record.TenantID)
	podName := firstNonEmpty(record.Name, record.InstanceID)
	switch request.Action {
	case ports.WorkloadInstanceOpsLogs:
		body, err := o.client.do(ctx, http.MethodGet, o.client.host+podPath(namespace, podName)+"/log?"+opsLogQuery(request), "", nil)
		return string(body), "", err
	case ports.WorkloadInstanceOpsEvents:
		query := "fieldSelector=" + url.QueryEscape("involvedObject.name="+podName)
		body, err := o.client.do(ctx, http.MethodGet, o.client.host+"/api/v1/namespaces/"+url.PathEscape(namespace)+"/events?"+query, "", nil)
		return string(body), "", err
	case ports.WorkloadInstanceOpsMetrics:
		body, err := o.client.do(ctx, http.MethodGet, o.client.host+"/apis/metrics.k8s.io/v1beta1/namespaces/"+url.PathEscape(namespace)+"/pods/"+url.PathEscape(podName), "", nil)
		return string(body), "", err
	case ports.WorkloadInstanceOpsTerminal, ports.WorkloadInstanceOpsExec:
		query := opsExecQuery(request)
		body, err := o.client.do(ctx, http.MethodPost, o.client.host+podPath(namespace, podName)+"/exec?"+query, "", nil)
		return string(body), opsSessionID(request), err
	case ports.WorkloadInstanceOpsVMConsole:
		body, err := o.client.doWithHeaders(
			ctx,
			http.MethodGet,
			o.client.host+kubeVirtSubresourcePath(namespace, podName, "console"),
			"",
			nil,
			kubeVirtSubresourceHeaders,
		)
		return string(body), opsSessionID(request), err
	case ports.WorkloadInstanceOpsVMVNC:
		body, err := o.client.doWithHeaders(
			ctx,
			http.MethodGet,
			o.client.host+kubeVirtSubresourcePath(namespace, podName, "vnc"),
			"",
			nil,
			kubeVirtSubresourceHeaders,
		)
		return string(body), opsSessionID(request), err
	case ports.WorkloadInstanceOpsVMSerial:
		body, err := o.client.doWithHeaders(
			ctx,
			http.MethodGet,
			o.client.host+kubeVirtSubresourcePath(namespace, podName, "console"),
			"",
			nil,
			kubeVirtSubresourceHeaders,
		)
		return string(body), opsSessionID(request), err
	default:
		return "", "", fmt.Errorf("%w: unsupported instance ops action %q", ports.ErrUnsupported, request.Action)
	}
}

func podPath(namespace string, podName string) string {
	return "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(podName)
}

func kubeVirtSubresourcePath(namespace string, vmName string, subresource string) string {
	return "/apis/subresources.kubevirt.io/v1/namespaces/" + url.PathEscape(namespace) + "/virtualmachineinstances/" + url.PathEscape(vmName) + "/" + url.PathEscape(subresource)
}

func opsLogQuery(request ports.WorkloadInstanceOpsRequest) string {
	values := url.Values{}
	if request.ContainerName != "" {
		values.Set("container", request.ContainerName)
	}
	if request.SinceSeconds > 0 {
		values.Set("sinceSeconds", strconv.FormatInt(request.SinceSeconds, 10))
	}
	if request.Limit > 0 {
		values.Set("tailLines", strconv.FormatInt(int64(request.Limit), 10))
	}
	return values.Encode()
}

func opsExecQuery(request ports.WorkloadInstanceOpsRequest) string {
	values := url.Values{}
	if request.ContainerName != "" {
		values.Set("container", request.ContainerName)
	}
	command := request.Command
	if len(command) == 0 && request.Action == ports.WorkloadInstanceOpsTerminal {
		command = []string{"/bin/sh"}
	}
	for _, arg := range command {
		values.Add("command", arg)
	}
	values.Set("stdin", "true")
	values.Set("stdout", "true")
	values.Set("stderr", "true")
	if request.Action == ports.WorkloadInstanceOpsTerminal {
		values.Set("tty", "true")
	}
	return values.Encode()
}

var _ ports.WorkloadInstanceOps = (*KubernetesInstanceOps)(nil)
