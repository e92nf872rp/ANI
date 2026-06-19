package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalGPUInventory struct {
	nodes []ports.GPUNodeClass
}

func NewLocalGPUInventory() *LocalGPUInventory {
	return &LocalGPUInventory{nodes: []ports.GPUNodeClass{
		{
			NodeName:      "ani-gpu-a",
			Vendor:        ports.GPUVendorNVIDIA,
			Model:         "A100",
			KernelVersion: "local-profile",
			OSImage:       "ANI Core dev profile",
			Pool:          "default",
			Labels:        map[string]string{"ani.io/gpu-demo": "true", "kubernetes.io/hostname": "ani-gpu-a", "nvidia.com/gpu.product": "A100"},
			Devices: []ports.GPUDeviceClass{
				{Vendor: ports.GPUVendorNVIDIA, Model: "A100", MemoryMiB: 40960, ResourceName: "nvidia.com/gpu", VirtualizationMode: ports.GPUVirtualizationNone, DriverVersion: "local-535", RuntimeVersion: "local", Capabilities: []string{"cuda", "compute"}},
				{Vendor: ports.GPUVendorNVIDIA, Model: "A100", MemoryMiB: 40960, ResourceName: "nvidia.com/gpu", VirtualizationMode: ports.GPUVirtualizationNone, DriverVersion: "local-535", RuntimeVersion: "local", Capabilities: []string{"cuda", "compute"}},
			},
			Ready:  true,
			Reason: "Core dev/local profile inventory; real GPU discovery is gated separately",
		},
		{
			NodeName:      "ani-gpu-b",
			Vendor:        ports.GPUVendorNVIDIA,
			Model:         "L40S",
			KernelVersion: "local-profile",
			OSImage:       "ANI Core dev profile",
			Pool:          "maintenance",
			Labels:        map[string]string{"ani.io/gpu-demo": "true", "kubernetes.io/hostname": "ani-gpu-b", "nvidia.com/gpu.product": "L40S"},
			Devices: []ports.GPUDeviceClass{
				{Vendor: ports.GPUVendorNVIDIA, Model: "L40S", MemoryMiB: 46080, ResourceName: "nvidia.com/gpu", VirtualizationMode: ports.GPUVirtualizationNone, DriverVersion: "local-535", RuntimeVersion: "local", Capabilities: []string{"cuda", "graphics"}},
			},
			Ready:  false,
			Reason: "local profile fault sample; not a production readiness signal",
		},
	}}
}

func (i *LocalGPUInventory) ListNodeClasses(_ context.Context, filter ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	nodes := make([]ports.GPUNodeClass, 0, len(i.nodes))
	for _, node := range i.nodes {
		if !matchesGPUDiscoveryFilter(node, filter) {
			continue
		}
		nodes = append(nodes, cloneGPUNodeClass(node))
	}
	return nodes, nil
}

func (i *LocalGPUInventory) GetNodeClass(_ context.Context, nodeName string) (ports.GPUNodeClass, error) {
	for _, node := range i.nodes {
		if node.NodeName == nodeName {
			return cloneGPUNodeClass(node), nil
		}
	}
	return ports.GPUNodeClass{}, ports.ErrNotFound
}

func (i *LocalGPUInventory) PlanScheduling(_ context.Context, request ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
	if strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.WorkloadID) == "" {
		return ports.GPUSchedulingDecision{}, fmt.Errorf("%w: tenant_id and workload_id are required", ports.ErrInvalid)
	}
	quantity := fmt.Sprintf("%d", positiveInt(request.RequiredCount, 1))
	return ports.GPUSchedulingDecision{
		NodeSelector:     map[string]string{"ani.io/gpu-demo": "true"},
		ResourceName:     "nvidia.com/gpu",
		ResourceQuantity: quantity,
		RuntimeClassName: "nvidia",
		SchedulerName:    "volcano",
		QueueName:        "local-gpu",
		Reasons:          []string{"local GPU scheduling decision; real GPU provider is gated separately"},
	}, nil
}

func matchesGPUDiscoveryFilter(node ports.GPUNodeClass, filter ports.GPUDiscoveryFilter) bool {
	if filter.Pool != "" && node.Pool != filter.Pool {
		return false
	}
	if len(filter.Vendors) > 0 {
		found := false
		for _, vendor := range filter.Vendors {
			if node.Vendor == vendor {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for key, value := range filter.Labels {
		if node.Labels[key] != value {
			return false
		}
	}
	return true
}

func cloneGPUNodeClass(node ports.GPUNodeClass) ports.GPUNodeClass {
	node.Labels = cloneGPUStringMap(node.Labels)
	node.Taints = append([]string(nil), node.Taints...)
	node.Devices = append([]ports.GPUDeviceClass(nil), node.Devices...)
	for index := range node.Devices {
		node.Devices[index].Capabilities = append([]string(nil), node.Devices[index].Capabilities...)
	}
	return node
}

func cloneGPUStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func positiveInt(value int, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

var _ ports.GPUInventory = (*LocalGPUInventory)(nil)
