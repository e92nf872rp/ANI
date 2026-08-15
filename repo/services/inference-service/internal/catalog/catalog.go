package catalog

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrModelNotFound       = errors.New("model version not found")
	ErrModelNotReady       = errors.New("model version is not ready for inference")
	ErrNoCompatibleProfile = errors.New("model version has no compatible inference profile")
)

type EngineProfile struct {
	ID       string
	Version  string
	Runtime  string
	ImageRef string
}

type ModelVersion struct {
	ID             uuid.UUID
	ModelID        uuid.UUID
	DisplayName    string
	Ready          bool
	Format         string
	SizeBytes      int64
	ArtifactRef    string
	ArtifactDigest string
	SecretRef      string
	EngineProfile  EngineProfile
	CPUProfile     *EngineProfile
	GPUProfile     *EngineProfile
}

type ModelCatalog interface {
	Resolve(context.Context, uuid.UUID, uuid.UUID) (ModelVersion, error)
}
