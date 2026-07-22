package registry

import (
	"context"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestWorkloadImageReferenceReaderListsMatchingActiveWorkloads(t *testing.T) {
	reader := NewWorkloadImageReferenceReader(fakeWorkloadInstanceStore{records: []ports.WorkloadInstanceRecord{
		{TenantID: "tenant-a", InstanceID: "container-a", Name: "api", Kind: ports.WorkloadKindContainer, Status: ports.WorkloadStatus{State: ports.WorkloadStateRunning}, Container: &ports.ContainerInstanceStatus{History: []ports.ContainerRevisionHistory{{Image: "harbor.harbor.svc.cluster.local/tenant-a/runtime:latest"}}}},
		{TenantID: "tenant-a", InstanceID: "gpu-a", Name: "trainer", Kind: ports.WorkloadKindGPUContainer, Status: ports.WorkloadStatus{State: ports.WorkloadStateStopped}, Container: &ports.ContainerInstanceStatus{History: []ports.ContainerRevisionHistory{{Image: "tenant-a/runtime:latest"}}}},
		{TenantID: "tenant-a", InstanceID: "gone", Name: "old", Kind: ports.WorkloadKindContainer, Status: ports.WorkloadStatus{State: ports.WorkloadStateDeleted}, Container: &ports.ContainerInstanceStatus{History: []ports.ContainerRevisionHistory{{Image: "tenant-a/runtime:latest"}}}},
	}})

	result, err := reader.ListRegistryImageReferences(context.Background(), ports.RegistryImageReferenceListRequest{TenantID: "tenant-a", Project: "tenant-a", Repository: "runtime", Tag: "latest"})
	if err != nil {
		t.Fatalf("ListRegistryImageReferences() error = %v", err)
	}
	if !result.DeleteBlocked || len(result.Items) != 2 {
		t.Fatalf("result = %+v, want two active references", result)
	}
	if result.Items[0].Route != "/instances/container-a" || result.Items[1].Kind != "gpu_container_instance" {
		t.Fatalf("references = %+v", result.Items)
	}
}

func TestWorkloadImageReferenceReaderFailsClosedWhenInstanceStoreFails(t *testing.T) {
	reader := NewWorkloadImageReferenceReader(fakeWorkloadInstanceStore{err: errors.New("metadata unavailable")})
	_, err := reader.ListRegistryImageReferences(context.Background(), ports.RegistryImageReferenceListRequest{TenantID: "tenant-a", Project: "tenant-a", Repository: "runtime", Tag: "latest"})
	if err == nil {
		t.Fatal("ListRegistryImageReferences() error = nil, want store failure")
	}
}

type fakeWorkloadInstanceStore struct {
	records []ports.WorkloadInstanceRecord
	err     error
}

func (s fakeWorkloadInstanceStore) UpsertStatus(context.Context, ports.WorkloadInstanceRecord) error {
	return s.err
}
func (s fakeWorkloadInstanceStore) Get(context.Context, string, string) (ports.WorkloadInstanceRecord, error) {
	return ports.WorkloadInstanceRecord{}, s.err
}
func (s fakeWorkloadInstanceStore) List(context.Context, string, ports.WorkloadKind) ([]ports.WorkloadInstanceRecord, error) {
	return s.records, s.err
}
