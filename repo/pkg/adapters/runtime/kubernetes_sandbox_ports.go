package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	sandboxPortServiceTTL      = time.Hour
	sandboxPortPreviewHostEnv  = "SANDBOX_PREVIEW_HOST"
	sandboxPortLabelManaged    = "ani.kubercloud.io/sandbox-preview"
	sandboxPortLabelTargetPort = "ani.kubercloud.io/sandbox-target-port"
)

var sandboxServiceNameSanitizer = regexp.MustCompile(`[^a-z0-9-]+`)

func (r *KubernetesSandboxRuntime) wirePortBackend() {
	if r.local == nil {
		return
	}
	r.local.portOpener = r.openPortService
	r.local.portCloser = r.closePortService
}

func (r *KubernetesSandboxRuntime) openPortService(ctx context.Context, request ports.SandboxPortRequest, instance ports.SandboxInstanceStatus) (ports.SandboxPortResult, error) {
	if !r.enabled || r.client == nil {
		return ports.SandboxPortResult{}, ports.ErrNotConfigured
	}
	if strings.TrimSpace(instance.Name) == "" {
		return ports.SandboxPortResult{}, fmt.Errorf("%w: sandbox instance name is required for preview ports", ports.ErrInvalid)
	}
	namespace := tenantNamespace(instance.TenantID)
	serviceName := sandboxPortServiceName(instance.Name, request.Port)
	manifest := ports.WorkloadManifest{
		Name:     serviceName,
		Kind:     "Service",
		Provider: "kubernetes",
		Content: mustJSON(map[string]any{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]any{
				"name":      serviceName,
				"namespace": namespace,
				"labels": map[string]string{
					"app.kubernetes.io/part-of":    "ani-platform",
					"ani.kubercloud.io/tenant-id":  instance.TenantID,
					"ani.kubercloud.io/instance":   instance.Name,
					sandboxPortLabelManaged:        "true",
					sandboxPortLabelTargetPort:     strconv.Itoa(request.Port),
				},
			},
			"spec": map[string]any{
				"type": "NodePort",
				"selector": map[string]string{
					"ani.kubercloud.io/tenant-id": instance.TenantID,
					"ani.kubercloud.io/instance":  instance.Name,
				},
				"ports": []any{
					map[string]any{
						"name":       firstNonEmpty(strings.TrimSpace(request.Name), fmt.Sprintf("p-%d", request.Port)),
						"protocol":   "TCP",
						"port":       request.Port,
						"targetPort": request.Port,
					},
				},
			},
		}),
	}
	_, _, hostIP, err := r.waitReadySandboxPodInfo(ctx, instance, sandboxFileExecTimeout)
	if err != nil {
		return ports.SandboxPortResult{}, err
	}
	if _, err := r.client.ApplyManifests(ctx, []ports.WorkloadManifest{manifest}); err != nil {
		return ports.SandboxPortResult{}, err
	}
	nodePort, err := r.readServiceNodePort(ctx, namespace, serviceName, request.Port)
	if err != nil {
		_ = r.deleteService(ctx, namespace, serviceName)
		return ports.SandboxPortResult{}, err
	}
	host, err := r.previewHost(ctx, hostIP)
	if err != nil {
		_ = r.deleteService(ctx, namespace, serviceName)
		return ports.SandboxPortResult{}, err
	}
	now := firstNonZeroTime(request.RequestedAt, r.now().UTC())
	previewURL := sandboxPreviewURL(request.Protocol, host, nodePort)
	return ports.SandboxPortResult{
		Port:       request.Port,
		Name:       strings.TrimSpace(request.Name),
		Protocol:   request.Protocol,
		Status:     "available",
		PreviewURL: previewURL,
		ExpiresAt:  now.Add(sandboxPortServiceTTL),
	}, nil
}

func (r *KubernetesSandboxRuntime) closePortService(ctx context.Context, request ports.SandboxPortDeleteRequest, instance ports.SandboxInstanceStatus, current ports.SandboxPortResult) (ports.SandboxPortResult, error) {
	if !r.enabled || r.client == nil {
		return ports.SandboxPortResult{}, ports.ErrNotConfigured
	}
	serviceName := sandboxPortServiceName(instance.Name, request.Port)
	if err := r.deleteService(ctx, tenantNamespace(instance.TenantID), serviceName); err != nil {
		return ports.SandboxPortResult{}, err
	}
	current.Status = "closing"
	return current, nil
}

func (r *KubernetesSandboxRuntime) cleanupPortServices(ctx context.Context, tenantID, instanceName string) error {
	if !r.enabled || r.client == nil || strings.TrimSpace(instanceName) == "" {
		return nil
	}
	namespace := tenantNamespace(tenantID)
	selector := url.Values{}
	selector.Set("labelSelector", fmt.Sprintf(
		"%s=true,%s=%s,%s=%s",
		sandboxPortLabelManaged,
		"ani.kubercloud.io/tenant-id", tenantID,
		"ani.kubercloud.io/instance", instanceName,
	))
	endpoint := r.client.host + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/services?" + selector.Encode()
	body, err := r.client.do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return err
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return fmt.Errorf("%w: decode sandbox preview services: %v", ports.ErrInvalid, err)
	}
	for _, item := range list.Items {
		if err := r.deleteService(ctx, namespace, item.Metadata.Name); err != nil {
			return err
		}
	}
	return nil
}

func (r *KubernetesSandboxRuntime) deleteService(ctx context.Context, namespace, name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	endpoint := r.client.host + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/services/" + url.PathEscape(name)
	_, status, err := r.client.Do(ctx, http.MethodDelete, endpoint, "", nil)
	if err != nil && status != http.StatusNotFound {
		return err
	}
	return nil
}

func (r *KubernetesSandboxRuntime) readServiceNodePort(ctx context.Context, namespace, name string, targetPort int) (int, error) {
	endpoint := r.client.host + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/services/" + url.PathEscape(name)
	body, err := r.client.do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		return 0, err
	}
	var doc struct {
		Spec struct {
			Ports []struct {
				Port     int `json:"port"`
				NodePort int `json:"nodePort"`
			} `json:"ports"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return 0, fmt.Errorf("%w: decode sandbox preview service: %v", ports.ErrInvalid, err)
	}
	for _, port := range doc.Spec.Ports {
		if port.Port == targetPort && port.NodePort > 0 {
			return port.NodePort, nil
		}
	}
	if len(doc.Spec.Ports) == 1 && doc.Spec.Ports[0].NodePort > 0 {
		return doc.Spec.Ports[0].NodePort, nil
	}
	return 0, fmt.Errorf("%w: sandbox preview service missing nodePort", ports.ErrFailedPrecondition)
}

func (r *KubernetesSandboxRuntime) previewHost(_ context.Context, podHostIP string) (string, error) {
	if host := strings.TrimSpace(os.Getenv(sandboxPortPreviewHostEnv)); host != "" {
		return host, nil
	}
	if host := strings.TrimSpace(podHostIP); host != "" {
		return host, nil
	}
	return "", fmt.Errorf("%w: unable to resolve sandbox preview host; set %s", ports.ErrFailedPrecondition, sandboxPortPreviewHostEnv)
}

func sandboxPortServiceName(instanceName string, port int) string {
	base := strings.ToLower(strings.TrimSpace(instanceName))
	base = sandboxServiceNameSanitizer.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "sandbox"
	}
	suffix := fmt.Sprintf("-p-%d", port)
	maxBase := 63 - len(suffix)
	if maxBase < 1 {
		maxBase = 1
	}
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
	}
	return base + suffix
}

func sandboxPreviewURL(protocol, host string, nodePort int) string {
	scheme := "http"
	if strings.EqualFold(protocol, "tcp") {
		scheme = "tcp"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, nodePort)
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}
