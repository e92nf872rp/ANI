package ports

import (
	"context"
	"time"
)

type SandboxTemplateListRequest struct {
	TenantID string
	Limit    int
	Cursor   string
}

type SandboxTemplateRecord struct {
	ID          string
	Name        string
	Image       string
	Description string
	CPUCores    *float64
	MemoryGB    *float64
	StorageGB   *float64
	IsBuiltin   bool
	CreatedAt   time.Time
	DevProfile  DevProfileInfo
}

type SandboxTemplateListResult struct {
	Items      []SandboxTemplateRecord
	Total      int
	NextCursor string
	DevProfile DevProfileInfo
}

// SandboxTemplateCatalog lists Core-owned local sandbox templates. It is kept
// separate from SandboxRuntime because templates are catalog metadata, not
// running sandbox lifecycle state.
type SandboxTemplateCatalog interface {
	ListSandboxTemplates(ctx context.Context, request SandboxTemplateListRequest) (SandboxTemplateListResult, error)
}
