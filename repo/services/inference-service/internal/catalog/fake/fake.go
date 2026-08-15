package fake

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
)

type key struct {
	tenantID  uuid.UUID
	versionID uuid.UUID
}

type Catalog struct {
	mu       sync.RWMutex
	versions map[key]catalog.ModelVersion
	lab      *catalog.ModelVersion
}

func New() *Catalog {
	return &Catalog{versions: make(map[key]catalog.ModelVersion)}
}

func NewLab(version catalog.ModelVersion) *Catalog {
	return &Catalog{versions: make(map[key]catalog.ModelVersion), lab: &version}
}

func (c *Catalog) Put(tenantID uuid.UUID, version catalog.ModelVersion) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.versions[key{tenantID: tenantID, versionID: version.ID}] = version
}

func (c *Catalog) Resolve(_ context.Context, tenantID, versionID uuid.UUID) (catalog.ModelVersion, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.lab != nil {
		version := *c.lab
		if versionID != uuid.Nil {
			version.ID = versionID
		}
		return version, nil
	}
	version, ok := c.versions[key{tenantID: tenantID, versionID: versionID}]
	if !ok {
		return catalog.ModelVersion{}, catalog.ErrModelNotFound
	}
	return version, nil
}
