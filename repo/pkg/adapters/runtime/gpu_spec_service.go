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

// gpuModelSpecID 生成 platform-workloads 广告用的型号 ID，例如 gpu-nvidia-geforce-rtx-4090。
// 不含 -full / -Nx；整卡或 vGPU 由创建请求的 memory 决定。
func gpuModelSpecID(gpuType string) string {
	value := strings.ToLower(strings.TrimSpace(gpuType))
	value = strings.NewReplacer(" ", "-", "/", "-", "_", "-").Replace(value)
	return "gpu-" + value
}

// gpuSpecID 只给 /gpu-specs 目录用，仍带 -full / -Nx。platform-workloads 不要再用这个 ID 广告。
func gpuSpecID(gpuType string, shares int) string {
	id := gpuModelSpecID(gpuType)
	if shares == 1 {
		return id + "-full"
	}
	return fmt.Sprintf("%s-%dx", id, shares)
}

var _ ports.GPUSpecService = (*LocalGPUSpecService)(nil)

// CompositeGPUSpecService implements ports.GPUSpecService by trying the CRD-backed
// GPUSpecStore first and falling back to a LocalGPUSpecService derived from node
// inventory. This lets instance creation resolve spec_ids that live as GPUSpec CRD
// instances (e.g. "rtx-4090-48g-1") while keeping the local dev profile working.
type CompositeGPUSpecService struct {
	store    ports.GPUSpecStore
	fallback ports.GPUSpecService
}

// NewCompositeGPUSpecService builds a composite spec service. When store is nil
// the composite delegates entirely to fallback, making it nil-safe for profiles
// without a CRD-backed spec store.
func NewCompositeGPUSpecService(store ports.GPUSpecStore, fallback ports.GPUSpecService) *CompositeGPUSpecService {
	return &CompositeGPUSpecService{store: store, fallback: fallback}
}

func (s *CompositeGPUSpecService) ListGPUSpecs(ctx context.Context, request ports.GPUSpecListRequest) ([]ports.GPUSpec, error) {
	if s.store != nil {
		crdSpecs, err := s.store.List(ctx)
		if err == nil {
			return crdSpecsToGPUSpecs(crdSpecs, request), nil
		}
	}
	if s.fallback != nil {
		return s.fallback.ListGPUSpecs(ctx, request)
	}
	return nil, ports.ErrNotFound
}

func (s *CompositeGPUSpecService) GetGPUSpec(ctx context.Context, specID string) (ports.GPUSpec, error) {
	specID = strings.TrimSpace(specID)
	if s.store != nil {
		crdSpec, err := s.store.Get(ctx, specID)
		if err == nil {
			return crdSpecToGPUSpec(crdSpec), nil
		}
	}
	if s.fallback != nil {
		return s.fallback.GetGPUSpec(ctx, specID)
	}
	return ports.GPUSpec{}, ports.ErrNotFound
}

func crdSpecsToGPUSpecs(crdSpecs []ports.GPUSpecCRD, request ports.GPUSpecListRequest) []ports.GPUSpec {
	available := true
	if request.Available != nil {
		available = *request.Available
	}
	items := make([]ports.GPUSpec, 0, len(crdSpecs))
	for _, crd := range crdSpecs {
		if strings.TrimSpace(request.GPUType) != "" && !strings.EqualFold(request.GPUType, crd.GPUType) {
			continue
		}
		spec := crdSpecToGPUSpec(crd)
		if spec.Available != available {
			continue
		}
		items = append(items, spec)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if request.Limit > 0 && len(items) > request.Limit {
		items = items[:request.Limit]
	}
	return items
}

func crdSpecToGPUSpec(crd ports.GPUSpecCRD) ports.GPUSpec {
	return ports.GPUSpec{
		ID:            crd.ID,
		Name:          crd.Name,
		GPUType:       crd.GPUType,
		MemoryTotalMB: crd.MemoryTotalMB,
		Shares:        crd.Shares,
		MBPerShare:    crd.MBPerShare,
		Available:     crd.Available,
	}
}

var _ ports.GPUSpecService = (*CompositeGPUSpecService)(nil)
