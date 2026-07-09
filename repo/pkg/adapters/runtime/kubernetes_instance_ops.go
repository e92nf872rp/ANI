package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesInstanceOps struct {
	client       *KubernetesRESTClient
	enabled      bool
	now          func() time.Time
	consoleBase  string
	mu           sync.RWMutex
	consoleSess  map[string]ports.WorkloadInstanceConsoleSession
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

func WithKubernetesInstanceOpsConsoleBaseURL(baseURL string) KubernetesInstanceOpsOption {
	return func(ops *KubernetesInstanceOps) {
		ops.consoleBase = strings.TrimSpace(baseURL)
	}
}

func NewKubernetesInstanceOps(client *KubernetesRESTClient, options ...KubernetesInstanceOpsOption) *KubernetesInstanceOps {
	ops := &KubernetesInstanceOps{
		client:      client,
		now:         time.Now,
		consoleSess: make(map[string]ports.WorkloadInstanceConsoleSession),
	}
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
	output, sessionID, token, connectURL, err := o.execute(ctx, request, record)
	if err != nil {
		return ports.WorkloadInstanceOpsResult{}, err
	}
	if connectURL == "" {
		connectURL = opsConnectURL(request, record, o.now().UTC())
	}
	return ports.WorkloadInstanceOpsResult{
		Action:     request.Action,
		Accepted:   true,
		SessionID:  sessionID,
		Protocol:   opsProtocol(request),
		ConnectURL: connectURL,
		URL:        connectURL,
		Token:      token,
		Output:     output,
		Reason:     "accepted by Kubernetes instance ops",
		CheckedAt:  o.now().UTC(),
		ExpiresAt:  o.now().UTC().Add(15 * time.Minute),
	}, nil
}

func (o *KubernetesInstanceOps) GetConsoleSession(_ context.Context, request ports.WorkloadInstanceConsoleSessionGetRequest) (ports.WorkloadInstanceConsoleSession, error) {
	if strings.TrimSpace(request.InstanceID) == "" {
		return ports.WorkloadInstanceConsoleSession{}, fmt.Errorf("%w: instance_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.SessionID) == "" {
		return ports.WorkloadInstanceConsoleSession{}, fmt.Errorf("%w: session_id is required", ports.ErrInvalid)
	}
	if strings.TrimSpace(request.Token) == "" {
		return ports.WorkloadInstanceConsoleSession{}, fmt.Errorf("%w: token is required", ports.ErrUnauthorized)
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, record := range o.consoleSess {
		if record.ID != request.SessionID {
			continue
		}
		if request.TenantID != "" && record.TenantID != request.TenantID {
			return ports.WorkloadInstanceConsoleSession{}, ports.ErrNotFound
		}
		if record.InstanceID != request.InstanceID {
			return ports.WorkloadInstanceConsoleSession{}, ports.ErrNotFound
		}
		if record.Token != request.Token {
			return ports.WorkloadInstanceConsoleSession{}, ports.ErrUnauthorized
		}
		if !o.now().UTC().Before(record.ExpiresAt) {
			return ports.WorkloadInstanceConsoleSession{}, ports.ErrExpired
		}
		return record, nil
	}
	return ports.WorkloadInstanceConsoleSession{}, ports.ErrNotFound
}

func (o *KubernetesInstanceOps) execute(ctx context.Context, request ports.WorkloadInstanceOpsRequest, record ports.WorkloadInstanceRecord) (string, string, string, string, error) {
	namespace := tenantNamespace(record.TenantID)
	podName := opsTargetName(record)
	switch request.Action {
	case ports.WorkloadInstanceOpsLogs:
		body, err := o.client.do(ctx, http.MethodGet, o.client.host+podPath(namespace, podName)+"/log?"+opsLogQuery(request), "", nil)
		return string(body), "", "", "", err
	case ports.WorkloadInstanceOpsEvents:
		query := "fieldSelector=" + url.QueryEscape("involvedObject.name="+podName)
		body, err := o.client.do(ctx, http.MethodGet, o.client.host+"/api/v1/namespaces/"+url.PathEscape(namespace)+"/events?"+query, "", nil)
		return string(body), "", "", "", err
	case ports.WorkloadInstanceOpsMetrics:
		body, err := o.client.do(ctx, http.MethodGet, o.client.host+"/apis/metrics.k8s.io/v1beta1/namespaces/"+url.PathEscape(namespace)+"/pods/"+url.PathEscape(podName), "", nil)
		return string(body), "", "", "", err
	case ports.WorkloadInstanceOpsTerminal, ports.WorkloadInstanceOpsExec:
		query := opsExecQuery(request)
		body, err := o.client.do(ctx, http.MethodPost, o.client.host+podPath(namespace, podName)+"/exec?"+query, "", nil)
		return string(body), opsSessionID(request), "", "", err
	case ports.WorkloadInstanceOpsVMConsole, ports.WorkloadInstanceOpsVMVNC, ports.WorkloadInstanceOpsVMSerial:
		session, err := o.issueConsoleSession(ctx, request, record)
		if err != nil {
			return "", "", "", "", err
		}
		return "", session.ID, session.Token, session.ConnectURL, nil
	default:
		return "", "", "", "", fmt.Errorf("%w: unsupported instance ops action %q", ports.ErrUnsupported, request.Action)
	}
}

func (o *KubernetesInstanceOps) issueConsoleSession(ctx context.Context, request ports.WorkloadInstanceOpsRequest, record ports.WorkloadInstanceRecord) (ports.WorkloadInstanceConsoleSession, error) {
	vmName := opsTargetName(record)
	if err := o.ensureVMIReady(ctx, record.TenantID, vmName); err != nil {
		return ports.WorkloadInstanceConsoleSession{}, err
	}
	token, err := newInstanceExecToken()
	if err != nil {
		return ports.WorkloadInstanceConsoleSession{}, err
	}
	now := o.now().UTC()
	sessionID := uuid.NewString()
	protocol := opsProtocol(request)
	session := ports.WorkloadInstanceConsoleSession{
		ID:          sessionID,
		TenantID:    record.TenantID,
		InstanceID:  record.InstanceID,
		VMName:      vmName,
		Protocol:    protocol,
		Subresource: consoleSubresource(request.Action),
		ConnectURL:  instanceConsoleWSURL(o.consoleBase, record.InstanceID, sessionID, token),
		Token:       token,
		ExpiresAt:   now.Add(15 * time.Minute),
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.consoleSess[sessionID] = session
	return session, nil
}

func (o *KubernetesInstanceOps) ensureVMIReady(ctx context.Context, tenantID string, vmName string) error {
	namespace := tenantNamespace(tenantID)
	endpoint := o.client.host + "/apis/kubevirt.io/v1/namespaces/" + url.PathEscape(namespace) + "/virtualmachineinstances/" + url.PathEscape(vmName)
	body, err := o.client.do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return err
	}
	var doc struct {
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("%w: decode virtualmachineinstance: %v", ports.ErrInvalid, err)
	}
	phase := strings.TrimSpace(doc.Status.Phase)
	if phase != "" && !strings.EqualFold(phase, "Running") {
		return fmt.Errorf("%w: virtualmachineinstance phase %q is not Running", ports.ErrConflict, phase)
	}
	return nil
}

func consoleSubresource(action ports.WorkloadInstanceOpsAction) string {
	switch action {
	case ports.WorkloadInstanceOpsVMVNC:
		return "vnc"
	default:
		return "console"
	}
}

func instanceConsoleWSURL(baseURL string, instanceID string, sessionID string, token string) string {
	u := normalizeExecWSBaseURL(baseURL) + "/instances/" + url.PathEscape(instanceID) + "/console/" + url.PathEscape(sessionID)
	values := url.Values{}
	values.Set("token", token)
	return u + "?" + values.Encode()
}

func opsTargetName(record ports.WorkloadInstanceRecord) string {
	ref, err := primaryWorkloadResourceRef(record.ResourceRefs)
	if err == nil {
		resource, err := resourceFromRecordRef(record, ref)
		if err == nil && strings.TrimSpace(resource.Name) != "" {
			return resource.Name
		}
	}
	return firstNonEmpty(record.Name, record.InstanceID)
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

var (
	_ ports.WorkloadInstanceOps                   = (*KubernetesInstanceOps)(nil)
	_ ports.WorkloadInstanceConsoleSessionStore    = (*KubernetesInstanceOps)(nil)
)
