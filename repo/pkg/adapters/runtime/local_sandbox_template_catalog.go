package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/pkg/ports"
)

type LocalSandboxTemplateCatalog struct {
	templates []ports.SandboxTemplateRecord
}

func NewLocalSandboxTemplateCatalog() *LocalSandboxTemplateCatalog {
	cpuSmall := 2.0
	memSmall := 4.0
	storageSmall := 20.0
	cpuGPU := 4.0
	memGPU := 16.0
	storageGPU := 80.0
	createdAt := time.Date(2026, 6, 19, 0, 0, 0, 0, time.UTC)
	return &LocalSandboxTemplateCatalog{templates: []ports.SandboxTemplateRecord{
		{
			ID:          uuid.NewString(),
			Name:        "python-secure",
			Image:       "registry.local/ani/sandbox-python:dev",
			Description: "Local Python sandbox template for Services integration development",
			CPUCores:    &cpuSmall,
			MemoryGB:    &memSmall,
			StorageGB:   &storageSmall,
			IsBuiltin:   true,
			CreatedAt:   createdAt,
			DevProfile:  sandboxTemplateCatalogDevProfile(),
		},
		{
			ID:          uuid.NewString(),
			Name:        "cuda-notebook-secure",
			Image:       "registry.local/ani/sandbox-cuda-notebook:dev",
			Description: "Local GPU-aware sandbox template; real runtime is gated separately",
			CPUCores:    &cpuGPU,
			MemoryGB:    &memGPU,
			StorageGB:   &storageGPU,
			IsBuiltin:   true,
			CreatedAt:   createdAt,
			DevProfile:  sandboxTemplateCatalogDevProfile(),
		},
	}}
}

func (c *LocalSandboxTemplateCatalog) ListSandboxTemplates(_ context.Context, request ports.SandboxTemplateListRequest) (ports.SandboxTemplateListResult, error) {
	if strings.TrimSpace(request.TenantID) == "" {
		return ports.SandboxTemplateListResult{}, fmt.Errorf("%w: tenant_id is required", ports.ErrInvalid)
	}
	limit := normalizeLimit(request.Limit, 20, 100)
	items := append([]ports.SandboxTemplateRecord(nil), c.templates...)
	if len(items) > limit {
		items = items[:limit]
	}
	return ports.SandboxTemplateListResult{Items: items, Total: len(items), DevProfile: sandboxTemplateCatalogDevProfile()}, nil
}

func sandboxTemplateCatalogDevProfile() ports.DevProfileInfo {
	return ports.DevProfileInfo{
		Mode:         "local",
		Provider:     "local-sandbox-template-catalog",
		RealProvider: false,
		Reason:       "Core dev/local profile sandbox template catalog; real sandbox runtime readiness is gated separately",
	}
}

var _ ports.SandboxTemplateCatalog = (*LocalSandboxTemplateCatalog)(nil)
