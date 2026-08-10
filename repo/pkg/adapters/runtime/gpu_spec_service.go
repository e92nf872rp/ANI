package runtime

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalGPUSpecService struct {
	inventory ports.GPUInventory
}

func NewLocalGPUSpecService(inventory ports.GPUInventory) *LocalGPUSpecService {
	if inventory == nil {
		inventory = NewLocalGPUInventory()
	}
	return &LocalGPUSpecService{inventory: inventory}
}

func (s *LocalGPUSpecService) ListGPUSpecs(ctx context.Context, request ports.GPUSpecListRequest) ([]ports.GPUSpec, error) {
	nodes, err := s.inventory.ListNodeClasses(ctx, ports.GPUDiscoveryFilter{})
	if err != nil {
		return nil, err
	}
	available := true
	if request.Available != nil {
		available = *request.Available
	}
	byID := map[string]ports.GPUSpec{}
	for _, node := range nodes {
		for _, device := range node.Devices {
			gpuType := firstNonEmpty(device.Model, node.Model)
			if strings.TrimSpace(request.GPUType) != "" && !strings.EqualFold(request.GPUType, gpuType) {
				continue
			}
			memory := device.MemoryMiB
			if memory < 1 {
				continue
			}
			fullID := gpuSpecID(gpuType, 1)
			fullSpec := ports.GPUSpec{ID: fullID, Name: gpuType + " Full", GPUType: gpuType, MemoryTotalMB: memory, Shares: 1, MBPerShare: int(memory), Available: node.Ready}
			if existing, ok := byID[fullID]; ok {
				fullSpec.Available = existing.Available || fullSpec.Available
				if existing.MemoryTotalMB > fullSpec.MemoryTotalMB {
					fullSpec.MemoryTotalMB = existing.MemoryTotalMB
					fullSpec.MBPerShare = int(existing.MemoryTotalMB)
				}
			}
			byID[fullID] = fullSpec
			if device.VirtualizationMode == ports.GPUVirtualizationVGPU || device.VirtualizationMode == ports.GPUVirtualizationMIG {
				shares := 4
				mbPerShare := int(memory) / shares
				if mbPerShare > 0 {
					id := gpuSpecID(gpuType, shares)
					vGPU := ports.GPUSpec{ID: id, Name: fmt.Sprintf("%s %dx", gpuType, shares), GPUType: gpuType, MemoryTotalMB: memory, Shares: shares, MBPerShare: mbPerShare, Available: node.Ready}
					if existing, ok := byID[id]; ok {
						vGPU.Available = existing.Available || vGPU.Available
						if existing.MemoryTotalMB > vGPU.MemoryTotalMB {
							vGPU.MemoryTotalMB = existing.MemoryTotalMB
							vGPU.MBPerShare = int(existing.MemoryTotalMB) / shares
						}
					}
					byID[id] = vGPU
				}
			}
		}
	}
	items := make([]ports.GPUSpec, 0, len(byID))
	for _, item := range byID {
		if item.Available != available {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if request.Limit > 0 && len(items) > request.Limit {
		items = items[:request.Limit]
	}
	return items, nil
}

func (s *LocalGPUSpecService) GetGPUSpec(ctx context.Context, specID string) (ports.GPUSpec, error) {
	all := false
	items, err := s.ListGPUSpecs(ctx, ports.GPUSpecListRequest{Available: &all})
	if err != nil {
		return ports.GPUSpec{}, err
	}
	for _, item := range items {
		if item.ID == strings.TrimSpace(specID) {
			return item, nil
		}
	}
	available := true
	items, err = s.ListGPUSpecs(ctx, ports.GPUSpecListRequest{Available: &available})
	if err != nil {
		return ports.GPUSpec{}, err
	}
	for _, item := range items {
		if item.ID == strings.TrimSpace(specID) {
			return item, nil
		}
	}
	return ports.GPUSpec{}, ports.ErrNotFound
}

func gpuSpecID(gpuType string, shares int) string {
	value := strings.ToLower(strings.TrimSpace(gpuType))
	value = strings.NewReplacer(" ", "-", "/", "-", "_", "-").Replace(value)
	if shares == 1 {
		return "gpu-" + value + "-full"
	}
	return fmt.Sprintf("gpu-%s-%dx", value, shares)
}

var _ ports.GPUSpecService = (*LocalGPUSpecService)(nil)
