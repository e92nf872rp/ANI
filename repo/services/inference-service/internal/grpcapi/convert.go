package grpcapi

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func parseTenantID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errUnauthenticated
	}
	return id, nil
}

func parseResourceID(raw, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%w: %s must be a UUID", errInvalidArgument, field)
	}
	return id, nil
}

func createInputFromProto(req *inferencecontrolv1.CreateInferenceServiceRequest) (service.CreateInput, error) {
	if req == nil {
		return service.CreateInput{}, errInvalidArgument
	}
	idempotencyKey, err := parseResourceID(req.GetIdempotencyKey(), "idempotency_key")
	if err != nil {
		return service.CreateInput{}, err
	}
	modelVersionID, err := parseModelVersionID(req.GetModel(), req.GetModelVersionId())
	if err != nil {
		return service.CreateInput{}, err
	}
	spec := domain.Spec{Replicas: int(req.GetReplicas()), PlacementMode: strings.TrimSpace(req.GetPlacementMode())}
	if resources := req.GetResources(); resources != nil {
		spec.CPU = strings.TrimSpace(resources.GetCpu())
		spec.Memory = strings.TrimSpace(resources.GetMemory())
		if acc := resources.GetAccelerator(); acc != nil && strings.TrimSpace(acc.GetSpecId()) != "" {
			spec.Accelerator = &domain.Accelerator{
				SpecID:          strings.TrimSpace(acc.GetSpecId()),
				CountPerReplica: int(acc.GetCountPerReplica()),
			}
		}
	}
	if spec.Accelerator == nil && strings.TrimSpace(req.GetGpuType()) != "" {
		count := int(req.GetGpuCountPerPod())
		if count == 0 {
			count = 1
		}
		spec.Accelerator = &domain.Accelerator{SpecID: strings.TrimSpace(req.GetGpuType()), CountPerReplica: count}
		spec.LegacyGPUType = strings.TrimSpace(req.GetGpuType())
	}
	spec.LegacyGPUCountPerPod = int(req.GetGpuCountPerPod())
	if strings.TrimSpace(req.GetName()) == "" || strings.TrimSpace(spec.CPU) == "" || strings.TrimSpace(spec.Memory) == "" {
		return service.CreateInput{}, errInvalidArgument
	}
	return service.CreateInput{
		IdempotencyKey:  idempotencyKey,
		Name:            strings.TrimSpace(req.GetName()),
		ModelVersionID:  modelVersionID,
		ServedModelName: strings.TrimSpace(req.GetServedModelName()),
		Spec:            spec,
	}, nil
}

func parseModelVersionID(model, explicit string) (uuid.UUID, error) {
	modelID, modelErr := uuid.Parse(strings.TrimSpace(model))
	if strings.TrimSpace(explicit) == "" {
		if modelErr != nil {
			return uuid.Nil, fmt.Errorf("%w: model must identify an immutable model version UUID", errInvalidArgument)
		}
		return modelID, nil
	}
	explicitID, err := uuid.Parse(strings.TrimSpace(explicit))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: model_version_id must be a UUID", errInvalidArgument)
	}
	if modelErr != nil || modelID != explicitID {
		return uuid.Nil, fmt.Errorf("%w: model and model_version_id must match", errInvalidArgument)
	}
	return explicitID, nil
}

func protoService(view service.ServiceView) *inferencecontrolv1.InferenceService {
	msg := &inferencecontrolv1.InferenceService{
		Id: view.ID.String(), Name: view.Name, Model: view.Model,
		ModelVersionId: view.ModelVersionID.String(), ServedModelName: view.ServedModelName,
		Replicas: int32(view.Replicas), ReadyReplicas: int32(view.ReadyReplicas),
		Resources: &inferencecontrolv1.InferenceServiceResources{
			Cpu: view.Resources.CPU, Memory: view.Resources.Memory,
		},
		PlacementMode: view.PlacementMode, GpuCountPerPod: int32(view.LegacyGPUCount),
		MaxConcurrency: int32(view.MaxConcurrency), Status: string(view.Status),
		Generation: view.Generation, ObservedGeneration: view.ObservedGeneration,
		CreatedAt: timestamppb.New(view.CreatedAt),
	}
	if view.Resources.Accelerator != nil {
		msg.Resources.Accelerator = &inferencecontrolv1.InferenceServiceAccelerator{
			SpecId: view.Resources.Accelerator.SpecID, CountPerReplica: int32(view.Resources.Accelerator.CountPerReplica),
		}
	}
	if view.LegacyGPUType != nil {
		msg.GpuType = *view.LegacyGPUType
	}
	if view.StatusReason != nil {
		msg.StatusReason = *view.StatusReason
	}
	if view.StatusMessage != nil {
		msg.StatusMessage = *view.StatusMessage
	}
	if view.CurrentOperationID != nil {
		msg.CurrentOperationId = view.CurrentOperationID.String()
	}
	if view.UpdatedAt != nil {
		msg.UpdatedAt = timestamppb.New(*view.UpdatedAt)
	}
	return msg
}

func protoOperation(view service.OperationView) *inferencecontrolv1.InferenceOperation {
	msg := &inferencecontrolv1.InferenceOperation{
		Id: view.ID.String(), TaskType: view.TaskType, ResourceType: view.ResourceType,
		ResourceId: view.ResourceID.String(), IdempotencyKey: view.IdempotencyKey,
		Status: string(view.Status), AttemptCount: int32(view.AttemptCount),
		ProgressPct: int32(view.ProgressPct), CreatedAt: timestamppb.New(view.CreatedAt),
	}
	if view.ErrorMessage != nil {
		msg.ErrorMessage = *view.ErrorMessage
	}
	if view.CompletedAt != nil {
		msg.CompletedAt = timestamppb.New(*view.CompletedAt)
	}
	return msg
}

func protoOperationFromDomain(operation domain.Operation) *inferencecontrolv1.InferenceOperation {
	return protoOperation(service.ProjectOperation(operation))
}

func protoLogPage(page service.LogPage) *inferencecontrolv1.ListInferenceServiceLogsResponse {
	items := make([]*inferencecontrolv1.InferenceServiceLogEntry, 0, len(page.Items))
	for _, item := range page.Items {
		entry := &inferencecontrolv1.InferenceServiceLogEntry{
			Level: item.Level, Message: item.Message, Container: item.Container, Stream: item.Stream,
		}
		if !item.Timestamp.IsZero() {
			entry.Timestamp = timestamppb.New(item.Timestamp.UTC())
		}
		items = append(items, entry)
	}
	return &inferencecontrolv1.ListInferenceServiceLogsResponse{Items: items, NextCursor: page.NextCursor}
}

func acceptedService(resource domain.Service, operation domain.Operation) *inferencecontrolv1.InferenceService {
	if resource.CurrentOperationID == uuid.Nil {
		resource.CurrentOperationID = operation.ID
	}
	return protoService(service.ProjectService(resource))
}
