package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/catalog"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
	"github.com/kubercloud/ani/services/inference-service/internal/runtime"
)

const createOperationScope = "inference_service.create"

type CreateInput struct {
	IdempotencyKey  uuid.UUID
	Name            string
	ModelVersionID  uuid.UUID
	ServedModelName string
	Spec            domain.Spec
}

type RuntimeAdmission interface {
	Admit(context.Context, uuid.UUID, domain.Spec) error
}

type Creator struct {
	store     repository.Store
	catalog   catalog.ModelCatalog
	admission RuntimeAdmission
	now       func() time.Time
}

func NewCreator(store repository.Store, modelCatalog catalog.ModelCatalog, now func() time.Time) *Creator {
	if now == nil {
		now = time.Now
	}
	return &Creator{store: store, catalog: modelCatalog, now: now}
}

func (c *Creator) WithAdmission(admission RuntimeAdmission) *Creator {
	c.admission = admission
	return c
}

func (c *Creator) Create(ctx context.Context, tenantID uuid.UUID, input CreateInput) (domain.Service, domain.Operation, error) {
	input = normalizeCreateInput(input)
	if err := validateCreateInput(tenantID, input); err != nil {
		return domain.Service{}, domain.Operation{}, err
	}
	requestHash, err := hashCreateInput(input)
	if err != nil {
		return domain.Service{}, domain.Operation{}, err
	}
	if replay, found, err := c.store.FindCreateReplay(ctx, tenantID, createOperationScope, input.IdempotencyKey, requestHash); err != nil {
		return domain.Service{}, domain.Operation{}, err
	} else if found {
		return replay.Service, replay.Operation, nil
	}
	version, err := c.catalog.Resolve(ctx, tenantID, input.ModelVersionID)
	if err != nil {
		return domain.Service{}, domain.Operation{}, err
	}
	if !version.Ready {
		return domain.Service{}, domain.Operation{}, catalog.ErrModelNotReady
	}
	if version.ID != input.ModelVersionID {
		return domain.Service{}, domain.Operation{}, catalog.ErrModelNotFound
	}
	profile, err := selectProfile(version, input.Spec)
	if err != nil {
		return domain.Service{}, domain.Operation{}, err
	}
	if c.admission != nil {
		if err := c.admission.Admit(ctx, tenantID, input.Spec); err != nil {
			if errors.Is(err, runtime.ErrRuntimeUnsupported) {
				return domain.Service{}, domain.Operation{}, ErrAcceleratorSpecUnavailable
			}
			return domain.Service{}, domain.Operation{}, err
		}
	}

	input.Spec.ExecutionProfile = domain.ExecutionProfile{
		ID:             profile.ID,
		Version:        profile.Version,
		Runtime:        profile.Runtime,
		ImageRef:       profile.ImageRef,
		ArtifactRef:    version.ArtifactRef,
		ArtifactDigest: version.ArtifactDigest,
		SecretRef:      version.SecretRef,
	}
	modelSnapshot, err := json.Marshal(struct {
		ModelID        uuid.UUID `json:"model_id"`
		VersionID      uuid.UUID `json:"version_id"`
		DisplayName    string    `json:"display_name"`
		Format         string    `json:"format"`
		SizeBytes      int64     `json:"size_bytes"`
		ArtifactDigest string    `json:"artifact_digest"`
	}{version.ModelID, version.ID, version.DisplayName, version.Format, version.SizeBytes, version.ArtifactDigest})
	if err != nil {
		return domain.Service{}, domain.Operation{}, fmt.Errorf("marshal model display snapshot: %w", err)
	}

	now := c.now().UTC()
	operationID := uuid.New()
	initial := domain.Service{
		ID:              uuid.New(),
		TenantID:        tenantID,
		Name:            input.Name,
		ModelVersionID:  input.ModelVersionID,
		ServedModelName: input.ServedModelName,
		ModelSnapshot:   modelSnapshot,
		Status:          domain.StatusPending,
		DesiredState:    domain.DesiredStateRunning,
		Generation:      0,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	transition, err := domain.BeginTransition(initial, domain.ActionCreate, input.Spec, operationID)
	if err != nil {
		return domain.Service{}, domain.Operation{}, err
	}
	resource, operation := transition.Service, transition.Operation
	operation.OperationScope = createOperationScope
	operation.IdempotencyKey = input.IdempotencyKey
	operation.RequestHash = requestHash
	operation.NextAttemptAt = now
	operation.CreatedAt = now
	operation.UpdatedAt = now

	result, err := c.store.CreateWithOperation(ctx, resource, operation)
	if err != nil {
		return domain.Service{}, domain.Operation{}, err
	}
	return result.Service, result.Operation, nil
}

func selectProfile(version catalog.ModelVersion, spec domain.Spec) (catalog.EngineProfile, error) {
	if spec.Accelerator != nil {
		if strings.TrimSpace(spec.Accelerator.SpecID) == "" || spec.Accelerator.CountPerReplica < 1 {
			return catalog.EngineProfile{}, fmt.Errorf("%w: accelerator spec id and positive count are required", ErrInvalidInput)
		}
		if spec.PlacementMode == "multi_node" && spec.Replicas != 1 {
			return catalog.EngineProfile{}, fmt.Errorf("%w: multi-node inference requires exactly one replica", ErrUnsupportedTopology)
		}
		if version.GPUProfile == nil {
			return catalog.EngineProfile{}, catalog.ErrNoCompatibleProfile
		}
		return *version.GPUProfile, nil
	}
	if version.CPUProfile == nil {
		return catalog.EngineProfile{}, catalog.ErrNoCompatibleProfile
	}
	return *version.CPUProfile, nil
}

func normalizeCreateInput(input CreateInput) CreateInput {
	input.Name = strings.TrimSpace(input.Name)
	input.ServedModelName = strings.TrimSpace(input.ServedModelName)
	if input.ServedModelName == "" {
		input.ServedModelName = input.Name
	}
	if input.Spec.Replicas == 0 {
		input.Spec.Replicas = 1
	}
	if input.Spec.PlacementMode == "" {
		input.Spec.PlacementMode = "auto"
	}
	return input
}

func validateCreateInput(tenantID uuid.UUID, input CreateInput) error {
	switch {
	case tenantID == uuid.Nil:
		return fmt.Errorf("%w: tenant id is required", ErrInvalidInput)
	case input.IdempotencyKey == uuid.Nil:
		return fmt.Errorf("%w: idempotency key is required", ErrInvalidInput)
	case input.ModelVersionID == uuid.Nil:
		return fmt.Errorf("%w: model version id is required", ErrInvalidInput)
	case input.Name == "":
		return fmt.Errorf("%w: inference service name is required", ErrInvalidInput)
	case input.Spec.Replicas < 1:
		return fmt.Errorf("%w: replicas must be positive", ErrInvalidInput)
	case strings.TrimSpace(input.Spec.CPU) == "":
		return fmt.Errorf("%w: cpu is required", ErrInvalidInput)
	case strings.TrimSpace(input.Spec.Memory) == "":
		return fmt.Errorf("%w: memory is required", ErrInvalidInput)
	case input.Spec.PlacementMode != "auto" && input.Spec.PlacementMode != "single_node" && input.Spec.PlacementMode != "multi_node":
		return fmt.Errorf("%w: placement mode must be auto, single_node, or multi_node", ErrInvalidInput)
	case input.Spec.Accelerator == nil && input.Spec.PlacementMode == "multi_node":
		return fmt.Errorf("%w: multi-node CPU inference is not supported", ErrUnsupportedTopology)
	case input.Spec.Accelerator != nil && strings.TrimSpace(input.Spec.Accelerator.SpecID) == "":
		return fmt.Errorf("%w: accelerator spec id is required", ErrInvalidInput)
	case input.Spec.Accelerator != nil && input.Spec.Accelerator.CountPerReplica < 1:
		return fmt.Errorf("%w: accelerator count must be positive", ErrInvalidInput)
	case input.Spec.Accelerator != nil && input.Spec.PlacementMode == "multi_node" && input.Spec.Replicas != 1:
		return fmt.Errorf("%w: multi-node inference requires exactly one replica", ErrUnsupportedTopology)
	default:
		return nil
	}
}

func hashCreateInput(input CreateInput) (string, error) {
	hashInput := struct {
		Name            string      `json:"name"`
		ModelVersionID  uuid.UUID   `json:"model_version_id"`
		ServedModelName string      `json:"served_model_name"`
		Spec            domain.Spec `json:"spec"`
	}{input.Name, input.ModelVersionID, input.ServedModelName, input.Spec}
	encoded, err := json.Marshal(hashInput)
	if err != nil {
		return "", fmt.Errorf("marshal normalized inference create request: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
