package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	platformWorkloadClassLabel   = "ani.platform_workload"
	platformWorkloadTenantLabel  = "ani.kubercloud.io/tenant-id"
	platformWorkloadIDLabel      = "ani.kubercloud.io/platform-workload"
	platformWorkloadNameLabel    = "ani.kubercloud.io/platform-workload-name"
	platformWorkloadOwnerLabel   = "ani.kubercloud.io/owner-ref"
	platformWorkloadRuntimeShape = "deployment"
	platformWorkloadProviderName = "kubernetes"
)

type platformWorkloadRuntime interface {
	Apply(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error)
	Observe(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error)
	Delete(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) error
	Logs(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, limit int, cursor, level string) (ports.PlatformWorkloadLogList, error)
}

type platformWorkloadObservation struct {
	ReadyReplicas int
	Endpoint      string
	Ready         bool
	Reason        string
}

type KubernetesPlatformWorkloadRuntime struct {
	client *KubernetesRESTClient
}

func NewKubernetesPlatformWorkloadRuntime(client *KubernetesRESTClient) *KubernetesPlatformWorkloadRuntime {
	return &KubernetesPlatformWorkloadRuntime{client: client}
}

func (r *KubernetesPlatformWorkloadRuntime) Apply(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error) {
	if r == nil || r.client == nil {
		return platformWorkloadObservation{}, fmt.Errorf("%w: kubernetes platform workload client is not configured", ports.ErrUnavailable)
	}
	nodeCIDRs, err := r.client.ListNodeInternalCIDRs(ctx)
	if err != nil {
		return platformWorkloadObservation{}, err
	}
	manifests := append([]ports.WorkloadManifest{renderPlatformWorkloadNamespace(tenantID)}, renderPlatformWorkloadManifests(tenantID, workloadID, spec, nodeCIDRs)...)
	if _, err := r.client.ApplyManifests(ctx, manifests); err != nil {
		return platformWorkloadObservation{}, err
	}
	return platformWorkloadObservation{
		Endpoint: platformWorkloadEndpoint(tenantID, spec),
		Reason:   "applied",
	}, nil
}

func (r *KubernetesPlatformWorkloadRuntime) Observe(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) (platformWorkloadObservation, error) {
	if r == nil || r.client == nil {
		return platformWorkloadObservation{}, fmt.Errorf("%w: kubernetes platform workload client is not configured", ports.ErrUnavailable)
	}
	_ = workloadID
	resource, err := resourceFromRef(platformWorkloadProviderName, tenantNamespace(tenantID), "kubernetes/Deployment/"+platformWorkloadResourceName(spec.Name))
	if err != nil {
		return platformWorkloadObservation{}, err
	}
	body, status, err := r.client.Do(ctx, http.MethodGet, r.client.resourceURL(resource, ""), "", nil)
	if err != nil {
		if status == http.StatusNotFound || isKubernetesNotFound(err) {
			return platformWorkloadObservation{Reason: "NotFound"}, nil
		}
		return platformWorkloadObservation{}, err
	}
	readyReplicas, err := readyReplicasFromDeployment(body)
	if err != nil {
		return platformWorkloadObservation{}, err
	}
	endpoint := platformWorkloadEndpoint(tenantID, spec)
	return platformWorkloadObservation{
		ReadyReplicas: readyReplicas,
		Endpoint:      endpoint,
		Ready:         readyReplicas >= spec.Replicas && spec.Replicas > 0 && endpoint != "",
	}, nil
}

func (r *KubernetesPlatformWorkloadRuntime) Delete(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) error {
	if r == nil || r.client == nil {
		return fmt.Errorf("%w: kubernetes platform workload client is not configured", ports.ErrUnavailable)
	}
	_ = workloadID
	namespace := tenantNamespace(tenantID)
	resourceName := platformWorkloadResourceName(spec.Name)
	for _, ref := range []string{
		"kubernetes/Service/" + resourceName,
		"kubernetes/NetworkPolicy/" + resourceName,
		"kubernetes/Deployment/" + resourceName,
	} {
		resource, err := resourceFromRef(platformWorkloadProviderName, namespace, ref)
		if err != nil {
			return err
		}
		_, status, err := r.client.Do(ctx, http.MethodDelete, r.client.resourceURL(resource, ""), "", nil)
		if err != nil && status != http.StatusNotFound && !isKubernetesNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *KubernetesPlatformWorkloadRuntime) Logs(ctx context.Context, tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, limit int, cursor, level string) (ports.PlatformWorkloadLogList, error) {
	if r == nil || r.client == nil {
		return ports.PlatformWorkloadLogList{}, fmt.Errorf("%w: kubernetes platform workload client is not configured", ports.ErrUnavailable)
	}
	_ = workloadID
	_ = cursor
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	namespace := tenantNamespace(tenantID)
	pods, err := r.listWorkloadPods(ctx, namespace, spec.Name)
	if err != nil {
		return ports.PlatformWorkloadLogList{}, err
	}
	items := make([]ports.PlatformWorkloadLogEntry, 0)
	for _, pod := range pods {
		query := url.Values{}
		query.Set("timestamps", "true")
		query.Set("tailLines", strconv.Itoa(limit))
		if pod.container != "" {
			query.Set("container", pod.container)
		}
		body, status, err := r.client.Do(ctx, http.MethodGet, r.client.host+podPath(namespace, pod.name)+"/log?"+query.Encode(), "", nil)
		if err != nil {
			if status == http.StatusNotFound || status == http.StatusBadRequest || isKubernetesNotFound(err) {
				continue
			}
			return ports.PlatformWorkloadLogList{}, err
		}
		items = append(items, parsePlatformWorkloadPodLogs(body, pod.container, level)...)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Timestamp.Before(items[j].Timestamp) })
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	return ports.PlatformWorkloadLogList{Items: items}, nil
}

type platformWorkloadPod struct {
	name      string
	container string
}

func (r *KubernetesPlatformWorkloadRuntime) listWorkloadPods(ctx context.Context, namespace, resourceName string) ([]platformWorkloadPod, error) {
	selector := url.QueryEscape(platformWorkloadNameLabel + "=" + resourceName)
	endpoint := r.client.host + "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods?labelSelector=" + selector
	body, status, err := r.client.Do(ctx, http.MethodGet, endpoint, "", nil)
	if err != nil {
		if status == http.StatusNotFound || isKubernetesNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: invalid Kubernetes pod list: %v", ports.ErrInvalid, err)
	}
	rawItems, _ := doc["items"].([]any)
	pods := make([]platformWorkloadPod, 0, len(rawItems))
	for _, raw := range rawItems {
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		metadata, _ := item["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		if strings.TrimSpace(name) == "" {
			continue
		}
		container := resourceName
		spec, _ := item["spec"].(map[string]any)
		containers, _ := spec["containers"].([]any)
		if len(containers) > 0 {
			first, _ := containers[0].(map[string]any)
			if value, _ := first["name"].(string); strings.TrimSpace(value) != "" {
				container = value
			}
		}
		pods = append(pods, platformWorkloadPod{name: name, container: container})
	}
	return pods, nil
}

func parsePlatformWorkloadPodLogs(body []byte, container, level string) []ports.PlatformWorkloadLogEntry {
	wanted := strings.ToLower(strings.TrimSpace(level))
	lines := strings.Split(string(body), "\n")
	items := make([]ports.PlatformWorkloadLogEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		timestamp := time.Now().UTC()
		message := line
		if stamp, rest, ok := strings.Cut(line, " "); ok {
			if parsed, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				timestamp = parsed.UTC()
				message = rest
			}
		}
		message = redactPlatformWorkloadLog(message)
		entryLevel := platformWorkloadLogLevel(message)
		if wanted != "" && entryLevel != wanted {
			continue
		}
		items = append(items, ports.PlatformWorkloadLogEntry{
			Timestamp: timestamp,
			Level:     entryLevel,
			Message:   message,
			Container: container,
			Stream:    "stdout",
		})
	}
	return items
}

func platformWorkloadLogLevel(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "fatal"):
		return "error"
	case strings.Contains(lower, "warn"):
		return "warn"
	case strings.Contains(lower, "debug"):
		return "debug"
	default:
		return "info"
	}
}

func redactPlatformWorkloadLog(message string) string {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "password") ||
		strings.Contains(message, "eyJ") {
		return "[redacted]"
	}
	return message
}

func renderPlatformWorkloadNamespace(tenantID string) ports.WorkloadManifest {
	name := tenantNamespace(tenantID)
	content := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]any{
			"name": name,
			"labels": map[string]string{
				"app.kubernetes.io/part-of": "ani-platform",
				platformWorkloadTenantLabel: tenantID,
			},
		},
	})
	return ports.WorkloadManifest{Name: name, Kind: "Namespace", Provider: platformWorkloadProviderName, Content: content}
}

func renderPlatformWorkloadManifests(tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, nodeCIDRs []string) []ports.WorkloadManifest {
	namespace := tenantNamespace(tenantID)
	resourceName := platformWorkloadResourceName(spec.Name)
	podLabels := platformWorkloadPodLabels(tenantID, workloadID, spec)
	selector := platformWorkloadSelectorLabels(tenantID, spec)
	healthName, healthPort := platformWorkloadHealthPort(spec)
	containerResources := platformWorkloadContainerResources(spec)
	containerPorts := make([]any, 0, len(spec.Network.Ports))
	servicePorts := make([]any, 0, len(spec.Network.Ports))
	for index, port := range spec.Network.Ports {
		name := strings.TrimSpace(port.Name)
		if name == "" {
			name = "port-" + strconv.Itoa(index+1)
		}
		containerPorts = append(containerPorts, map[string]any{
			"name":          name,
			"containerPort": port.Port,
			"protocol":      "TCP",
		})
		servicePorts = append(servicePorts, map[string]any{
			"name":       name,
			"port":       port.Port,
			"targetPort": port.Port,
			"protocol":   "TCP",
		})
	}
	probe := map[string]any{
		"httpGet": map[string]any{
			"path": spec.HealthCheck.Path,
			"port": healthName,
		},
		"periodSeconds":    10,
		"timeoutSeconds":   3,
		"failureThreshold": 90,
	}
	if healthName == "" {
		probe["httpGet"].(map[string]any)["port"] = healthPort
	}
	volumes, volumeMounts := platformWorkloadPodVolumes(spec)
	container := map[string]any{
		"name":            resourceName,
		"image":           spec.ImageRef,
		"imagePullPolicy": "IfNotPresent",
		"command":         omitEmptySlice(spec.Command),
		"args":            omitEmptySlice(spec.Args),
		"ports":           containerPorts,
		"resources":       containerResources,
		"volumeMounts":    volumeMounts,
		"readinessProbe":  probe,
	}
	deployment := manifest(map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata": map[string]any{
			"name":      resourceName,
			"namespace": namespace,
			"labels":    podLabels,
		},
		"spec": map[string]any{
			"replicas": spec.Replicas,
			"selector": map[string]any{"matchLabels": selector},
			"template": map[string]any{
				"metadata": map[string]any{"labels": podLabels},
				"spec": map[string]any{
					"restartPolicy": "Always",
					"containers":    []any{container},
					"volumes":       volumes,
				},
			},
		},
	})
	service := manifest(map[string]any{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]any{
			"name":      resourceName,
			"namespace": namespace,
			"labels":    podLabels,
		},
		"spec": map[string]any{
			"type":     "ClusterIP",
			"selector": selector,
			"ports":    servicePorts,
		},
	})
	return []ports.WorkloadManifest{
		{Name: resourceName, Kind: "Deployment", Provider: platformWorkloadProviderName, Content: deployment},
		{Name: resourceName, Kind: "Service", Provider: platformWorkloadProviderName, Content: service},
		renderPlatformWorkloadNetworkPolicy(tenantID, workloadID, spec, nodeCIDRs),
	}
}

func renderPlatformWorkloadNetworkPolicy(tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec, nodeCIDRs []string) ports.WorkloadManifest {
	namespace := tenantNamespace(tenantID)
	resourceName := platformWorkloadResourceName(spec.Name)
	selector := platformWorkloadSelectorLabels(tenantID, spec)
	policyPorts := make([]any, 0, len(spec.Network.Ports))
	for _, port := range spec.Network.Ports {
		policyPorts = append(policyPorts, map[string]any{
			"protocol": "TCP",
			"port":     port.Port,
		})
	}
	if len(policyPorts) == 0 {
		policyPorts = append(policyPorts, map[string]any{"protocol": "TCP", "port": 8000})
	}
	content := manifest(map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata": map[string]any{
			"name":      resourceName,
			"namespace": namespace,
			"labels":    platformWorkloadPodLabels(tenantID, workloadID, spec),
		},
		"spec": map[string]any{
			"podSelector": map[string]any{"matchLabels": selector},
			"policyTypes": []any{"Ingress"},
			"ingress": []any{
				map[string]any{
					"from":  platformWorkloadNetworkPolicyFrom(nodeCIDRs),
					"ports": policyPorts,
				},
			},
		},
	})
	return ports.WorkloadManifest{Name: resourceName, Kind: "NetworkPolicy", Provider: platformWorkloadProviderName, Content: content}
}

func platformWorkloadPodVolumes(spec ports.PlatformWorkloadCreateSpec) ([]any, []any) {
	volumes := []any{
		map[string]any{"name": "shm", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": "1Gi"}},
	}
	mounts := []any{
		map[string]any{"name": "shm", "mountPath": "/dev/shm"},
	}
	for index, artifact := range spec.Artifacts {
		claim, ok := pvcClaimName(artifact.ObjectRef)
		if !ok {
			continue
		}
		name := "artifact-" + strconv.Itoa(index+1)
		path := strings.TrimSpace(artifact.MountPath)
		if path == "" {
			path = "/models"
		}
		volumes = append(volumes, map[string]any{
			"name":                  name,
			"persistentVolumeClaim": map[string]any{"claimName": claim},
		})
		mounts = append(mounts, map[string]any{"name": name, "mountPath": path})
	}
	return volumes, mounts
}

func pvcClaimName(objectRef string) (string, bool) {
	ref := strings.TrimSpace(objectRef)
	ref, _, _ = strings.Cut(ref, "#")
	name, ok := strings.CutPrefix(ref, "pvc://")
	name = strings.TrimSpace(name)
	return name, ok && name != ""
}

func platformWorkloadContainerResources(spec ports.PlatformWorkloadCreateSpec) map[string]any {
	requests := map[string]any{"cpu": spec.Resources.CPU, "memory": spec.Resources.Memory}
	if spec.Resources.AcceleratorCount > 0 {
		requests["nvidia.com/gpu"] = strconv.Itoa(spec.Resources.AcceleratorCount)
	}
	return map[string]any{"requests": requests, "limits": requests}
}

func platformWorkloadPodLabels(tenantID, workloadID string, spec ports.PlatformWorkloadCreateSpec) map[string]string {
	labels := mergeStringMap(spec.Metadata.Labels, map[string]string{
		"app.kubernetes.io/part-of":    "ani-platform",
		"app.kubernetes.io/name":       "platform-workload",
		"app.kubernetes.io/component":  "runtime",
		"app.kubernetes.io/managed-by": "ani-platform-workload",
		platformWorkloadClassLabel:     spec.WorkloadClass,
		platformWorkloadTenantLabel:    tenantID,
		platformWorkloadIDLabel:        workloadID,
		platformWorkloadNameLabel:      spec.Name,
		platformWorkloadOwnerLabel:     spec.Metadata.OwnerRef,
	})
	if spec.Resources.AcceleratorSpecID != "" {
		labels["ani.kubercloud.io/accelerator-spec-id"] = spec.Resources.AcceleratorSpecID
	}
	return labels
}

func platformWorkloadSelectorLabels(tenantID string, spec ports.PlatformWorkloadCreateSpec) map[string]string {
	return map[string]string{
		platformWorkloadClassLabel:  spec.WorkloadClass,
		platformWorkloadTenantLabel: tenantID,
		platformWorkloadNameLabel:   spec.Name,
	}
}

func platformWorkloadHealthPort(spec ports.PlatformWorkloadCreateSpec) (string, int) {
	for _, port := range spec.Network.Ports {
		if port.Name == spec.HealthCheck.PortName {
			return port.Name, port.Port
		}
	}
	if len(spec.Network.Ports) > 0 {
		return spec.Network.Ports[0].Name, spec.Network.Ports[0].Port
	}
	return "http", 8000
}

func platformWorkloadEndpoint(tenantID string, spec ports.PlatformWorkloadCreateSpec) string {
	_, port := platformWorkloadHealthPort(spec)
	if len(spec.Network.Ports) > 0 {
		port = spec.Network.Ports[0].Port
	}
	return "http://" + platformWorkloadResourceName(spec.Name) + "." + tenantNamespace(tenantID) + ".svc:" + strconv.Itoa(port)
}

func platformWorkloadNetworkPolicyFrom(nodeCIDRs []string) []any {
	from := []any{
		map[string]any{"podSelector": map[string]any{}},
		map[string]any{"namespaceSelector": map[string]any{
			"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"},
		}},
		map[string]any{"namespaceSelector": map[string]any{
			"matchLabels": map[string]any{"kubernetes.io/metadata.name": "ani-system"},
		}},
	}
	for _, cidr := range platformWorkloadNodeCIDRs(nodeCIDRs) {
		from = append(from, map[string]any{"ipBlock": map[string]any{"cidr": cidr}})
	}
	return from
}

func platformWorkloadNodeCIDRs(cidrs []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(cidrs))
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			raw += "/32"
		}
		ip, ipnet, err := net.ParseCIDR(raw)
		if err != nil || ip == nil || ip.To4() == nil || ipnet == nil {
			continue
		}
		ones, bits := ipnet.Mask.Size()
		if ones != 32 || bits != 32 {
			continue
		}
		cidr := ip.To4().String() + "/32"
		if _, ok := seen[cidr]; ok {
			continue
		}
		seen[cidr] = struct{}{}
		out = append(out, cidr)
	}
	sort.Strings(out)
	return out
}

func platformWorkloadResourceName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "pw"
	}
	if name[0] < 'a' || name[0] > 'z' {
		return "pw-" + name
	}
	return name
}

func readyReplicasFromDeployment(body []byte) (int, error) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return 0, fmt.Errorf("%w: invalid Kubernetes deployment observation: %v", ports.ErrInvalid, err)
	}
	status, _ := doc["status"].(map[string]any)
	return jsonInt(status["readyReplicas"]), nil
}

func jsonInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

var _ platformWorkloadRuntime = (*KubernetesPlatformWorkloadRuntime)(nil)
