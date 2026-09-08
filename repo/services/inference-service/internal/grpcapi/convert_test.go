package grpcapi

import (
	"testing"
	"time"

	inferencecontrolv1 "github.com/kubercloud/ani/pkg/generated/pb/inference/control/v1"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/service"
)

func TestProtoAccessPolicyAndEventPreserveContractTimestamps(t *testing.T) {
	createdAt := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	updatedAt := createdAt.Add(time.Minute)
	policy := protoAccessPolicy(domain.AccessPolicy{
		ID: testService, TenantID: testTenant, Name: "policy", Status: domain.AccessPolicyEnabled,
		Priority: 1, Scope: domain.AccessPolicyScope{Type: domain.ScopeTenantDefault},
		Access:      domain.AccessPolicyAccess{AllowAllTenantKeys: true},
		Concurrency: domain.AccessPolicyConcurrency{LeaseTTLSeconds: 60},
		CreatedAt:   createdAt, UpdatedAt: updatedAt,
	})
	if policy.GetCreatedAt() == nil || !policy.GetCreatedAt().AsTime().Equal(createdAt) || policy.GetUpdatedAt() == nil || !policy.GetUpdatedAt().AsTime().Equal(updatedAt) {
		t.Fatalf("policy timestamps = (%v,%v)", policy.GetCreatedAt(), policy.GetUpdatedAt())
	}
	event := protoAccessPolicyEvent(domain.AccessPolicyEvent{ID: testService, TenantID: testTenant, CreatedAt: createdAt})
	if event.GetCreatedAt() == nil || !event.GetCreatedAt().AsTime().Equal(createdAt) {
		t.Fatalf("event created_at = %v", event.GetCreatedAt())
	}
}

func TestCreateInputFromProtoMapsAcceleratorMemory(t *testing.T) {
	input, err := createInputFromProto(&inferencecontrolv1.CreateInferenceServiceRequest{
		TenantId: testTenant.String(), IdempotencyKey: testKey.String(), Name: "qwen-chat",
		Model: testModel.String(), ImageRef: pinnedImage,
		Resources: &inferencecontrolv1.InferenceServiceResources{
			Cpu: "8", Memory: "32Gi",
			Accelerator: &inferencecontrolv1.InferenceServiceAccelerator{
				SpecId: "gpu-nvidia-geforce-rtx-4090", CountPerReplica: 1, Memory: 10240,
			},
		},
	})
	if err != nil {
		t.Fatalf("createInputFromProto() error = %v", err)
	}
	acc := input.Spec.Accelerator
	if acc == nil || acc.SpecID != "gpu-nvidia-geforce-rtx-4090" || acc.CountPerReplica != 1 || acc.MemoryMB != 10240 {
		t.Fatalf("accelerator = %+v", acc)
	}
}

func TestProtoServiceEchoesAcceleratorMemory(t *testing.T) {
	msg := protoService(service.ServiceView{
		ID: testService, Name: "qwen-chat", Model: testModel.String(), ModelVersionID: testModel,
		Replicas: 1,
		Resources: service.ResourcesView{
			CPU: "8", Memory: "32Gi",
			Accelerator: &domain.Accelerator{SpecID: "gpu-nvidia-geforce-rtx-4090", CountPerReplica: 1, MemoryMB: 10240},
		},
		CreatedAt: time.Now().UTC(),
	})
	if msg.GetResources().GetAccelerator().GetMemory() != 10240 {
		t.Fatalf("memory = %d", msg.GetResources().GetAccelerator().GetMemory())
	}
}

func TestProtoServiceProjectsPublishedInvocationURL(t *testing.T) {
	url := "https://ai.example.com/v1/chat/completions"
	msg := protoService(service.ServiceView{
		ID: testService, Name: "qwen-chat", Model: testModel.String(), ModelVersionID: testModel,
		InvocationURL: &url, CreatedAt: time.Now().UTC(),
	})
	if msg.GetInvocationUrl() != url {
		t.Fatalf("invocation_url = %q", msg.GetInvocationUrl())
	}
}
