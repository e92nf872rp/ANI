package service

import (
	"testing"

	modelv1 "github.com/kubercloud/ani/pkg/generated/pb/model/v1"
)

// This test is intentionally written against the additive P0 contract before
// the protobuf is changed. It should fail to compile until the new fields and
// RPC messages exist.
func TestModelRepositoryP0ContractHasIdempotencyAndVersionListing(t *testing.T) {
	create := &modelv1.CreateModelRequest{IdempotencyKey: "model-create-1"}
	if create.GetIdempotencyKey() != "model-create-1" {
		t.Fatalf("idempotency key was not retained")
	}
	version := &modelv1.CreateModelVersionRequest{IdempotencyKey: "version-create-1"}
	if version.GetIdempotencyKey() != "version-create-1" {
		t.Fatalf("version idempotency key was not retained")
	}
	upload := &modelv1.GetUploadURLRequest{IdempotencyKey: "upload-1"}
	if upload.GetIdempotencyKey() != "upload-1" {
		t.Fatalf("upload idempotency key was not retained")
	}
	list := &modelv1.ListModelsRequest{Source: "upload", Capability: "embedding", Keyword: "qwen"}
	if list.GetSource() != "upload" || list.GetCapability() != "embedding" || list.GetKeyword() != "qwen" {
		t.Fatalf("model list filters were not retained")
	}
	versions := &modelv1.ListModelVersionsRequest{TenantId: "tenant", ModelId: "model"}
	if versions.GetModelId() != "model" {
		t.Fatalf("version list request was not generated")
	}
}
