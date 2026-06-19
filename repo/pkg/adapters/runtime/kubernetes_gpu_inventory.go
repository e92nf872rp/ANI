package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

const (
	kubernetesNVIDIAGPUResource     = "nvidia.com/gpu"
	kubernetesNVIDIAGPUProductLabel = "nvidia.com/gpu.product"
	kubernetesHostnameLabel         = "kubernetes.io/hostname"
	kubernetesANIGPUPoolLabel       = "ani.kubercloud.io/gpu-pool"
)

type KubernetesGPUInventory struct {
	client *KubernetesRESTClient
}

func NewKubernetesGPUInventory(client *KubernetesRESTClient) *KubernetesGPUInventory {
	return &KubernetesGPUInventory{client: client}
}

func (i *KubernetesGPUInventory) ListNodeClasses(ctx context.Context, filter ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	if i.client == nil {
		return nil, fmt.Errorf("%w: Kubernetes REST client is required for GPU inventory", ports.ErrNotConfigured)
	}
	body, err := i.client.do(ctx, http.MethodGet, strings.TrimRight(i.client.host, "/")+"/api/v1/nodes", "", nil)
	if err != nil {
		return nil, err
	}
	nodes, err := gpuNodeClassesFromKubernetesNodeList(body)
	if err != nil {
		return nil, err
	}
	filtered := make([]ports.GPUNodeClass, 0, len(nodes))
	for _, node := range nodes {
		if matchesGPUDiscoveryFilter(node, filter) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (i *KubernetesGPUInventory) GetNodeClass(ctx context.Context, nodeName string) (ports.GPUNodeClass, error) {
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return ports.GPUNodeClass{}, fmt.Errorf("%w: node_name is required", ports.ErrInvalid)
	}
	nodes, err := i.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{Labels: map[string]string{kubernetesHostnameLabel: nodeName}})
	if err != nil {
		return ports.GPUNodeClass{}, err
	}
	for _, node := range nodes {
		if node.NodeName == nodeName {
			return node, nil
		}
	}
	return ports.GPUNodeClass{}, ports.ErrNotFound
}

func (i *KubernetesGPUInventory) PlanScheduling(ctx context.Context, request ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.WorkloadID) == "" {
		return ports.GPUSchedulingDecision{}, fmt.Errorf("%w: tenant_id and workload_id are required", ports.ErrInvalid)
	}
	requiredCount := positiveInt(request.RequiredCount, 1)
	nodes, err := i.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{Vendors: request.PreferredVendors, Pool: request.Pool})
	if err != nil {
		return ports.GPUSchedulingDecision{}, err
	}
	for _, node := range nodes {
		if !node.Ready || len(node.Devices) < requiredCount {
			continue
		}
		if !gpuNodeSupportsSchedulingRequest(node, request) {
			continue
		}
		return ports.GPUSchedulingDecision{
			NodeSelector: map[string]string{
				kubernetesHostnameLabel: node.NodeName,
			},
			ResourceName:     kubernetesNVIDIAGPUResource,
			ResourceQuantity: strconv.Itoa(requiredCount),
			RuntimeClassName: "nvidia",
			Reasons:          []string{"Kubernetes node inventory matched NVIDIA device-plugin capacity"},
		}, nil
	}
	return ports.GPUSchedulingDecision{}, fmt.Errorf("%w: no ready GPU node satisfies scheduling request", ports.ErrNotFound)
}

type kubernetesNodeListDocument struct {
	Items []kubernetesNodeDocument `json:"items"`
}

type kubernetesNodeDocument struct {
	Metadata kubernetesObjectMetadata `json:"metadata"`
	Status   kubernetesNodeStatus     `json:"status"`
}

type kubernetesObjectMetadata struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type kubernetesNodeStatus struct {
	Capacity    map[string]string         `json:"capacity"`
	Allocatable map[string]string         `json:"allocatable"`
	NodeInfo    kubernetesNodeSystemInfo  `json:"nodeInfo"`
	Conditions  []kubernetesNodeCondition `json:"conditions"`
}

type kubernetesNodeSystemInfo struct {
	KernelVersion  string `json:"kernelVersion"`
	OSImage        string `json:"osImage"`
	KubeletVersion string `json:"kubeletVersion"`
}

type kubernetesNodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func gpuNodeClassesFromKubernetesNodeList(body []byte) ([]ports.GPUNodeClass, error) {
	var doc kubernetesNodeListDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%w: invalid Kubernetes node list response: %v", ports.ErrInvalid, err)
	}
	nodes := make([]ports.GPUNodeClass, 0, len(doc.Items))
	for _, item := range doc.Items {
		count := gpuCapacity(item.Status.Capacity, item.Status.Allocatable)
		if count <= 0 {
			continue
		}
		nodeName := firstNonEmpty(item.Metadata.Labels[kubernetesHostnameLabel], item.Metadata.Name)
		model := firstNonEmpty(item.Metadata.Labels[kubernetesNVIDIAGPUProductLabel], "nvidia-gpu")
		devices := make([]ports.GPUDeviceClass, 0, count)
		for range count {
			devices = append(devices, ports.GPUDeviceClass{
				Vendor:             ports.GPUVendorNVIDIA,
				Model:              model,
				ResourceName:       kubernetesNVIDIAGPUResource,
				VirtualizationMode: ports.GPUVirtualizationNone,
				DriverVersion:      firstNonEmpty(item.Metadata.Labels["nvidia.com/cuda.driver.major"], "device-plugin"),
				RuntimeVersion:     item.Status.NodeInfo.KubeletVersion,
				Capabilities:       []string{"cuda", "compute"},
			})
		}
		ready, reason := kubernetesNodeReady(item.Status.Conditions)
		nodes = append(nodes, ports.GPUNodeClass{
			NodeName:      nodeName,
			Vendor:        ports.GPUVendorNVIDIA,
			Model:         model,
			KernelVersion: item.Status.NodeInfo.KernelVersion,
			OSImage:       item.Status.NodeInfo.OSImage,
			Pool:          firstNonEmpty(item.Metadata.Labels[kubernetesANIGPUPoolLabel], "default"),
			Labels:        cloneGPUStringMap(item.Metadata.Labels),
			Devices:       devices,
			Ready:         ready,
			Reason:        reason,
		})
	}
	return nodes, nil
}

func gpuCapacity(capacity map[string]string, allocatable map[string]string) int {
	value := strings.TrimSpace(capacity[kubernetesNVIDIAGPUResource])
	if value == "" {
		value = strings.TrimSpace(allocatable[kubernetesNVIDIAGPUResource])
	}
	count, err := strconv.Atoi(value)
	if err != nil || count < 0 {
		return 0
	}
	return count
}

func kubernetesNodeReady(conditions []kubernetesNodeCondition) (bool, string) {
	for _, condition := range conditions {
		if condition.Type != "Ready" {
			continue
		}
		if condition.Status == "True" {
			return true, firstNonEmpty(condition.Reason, "KubeletReady")
		}
		return false, firstNonEmpty(condition.Reason, condition.Message, "Kubernetes node is not ready")
	}
	return false, "Kubernetes node Ready condition not found"
}

func gpuNodeSupportsSchedulingRequest(node ports.GPUNodeClass, request ports.GPUSchedulingRequest) bool {
	if len(request.PreferredModels) > 0 {
		found := false
		for _, model := range request.PreferredModels {
			if strings.EqualFold(node.Model, strings.TrimSpace(model)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if request.RequiredMemoryMiB > 0 {
		for _, device := range node.Devices {
			if device.MemoryMiB >= request.RequiredMemoryMiB {
				return true
			}
		}
		return false
	}
	return true
}

var _ ports.GPUInventory = (*KubernetesGPUInventory)(nil)
