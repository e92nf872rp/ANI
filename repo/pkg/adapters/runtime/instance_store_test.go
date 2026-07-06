package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMetadataInstanceStoreUpsertsStatus(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataInstanceStore(fakeMetadataStore{tx: tx}, WithInstanceStoreClock(func() time.Time {
		return time.Unix(600, 0)
	}))

	err := store.UpsertStatus(context.Background(), ports.WorkloadInstanceRecord{
		TenantID:   "5dbb1d01-0000-4000-8000-000000000001",
		InstanceID: "inst_1",
		Name:       "app-01",
		Kind:       ports.WorkloadKindContainer,
		VPCID:      "vpc-a",
		SubnetID:   "subnet-a",
		PrivateIP:  "10.72.0.10",
		Provider:   "kubernetes",
		AuditID:    "5dbb1d01-0000-4000-8000-000000000002",
		Lifecycle: ports.InstanceLifecyclePolicy{
			TerminationProtection: true,
		},
		SSH: &ports.VMSSHConnectionInfo{
			Username: "ubuntu",
			Host:     "inst_1.vm.ani.internal",
			Port:     22,
			KeyRef:   "secret/ssh-key-a",
			Ready:    true,
		},
		Snapshots: []ports.VMInstanceSnapshot{
			{
				ID:               "snap-a",
				Name:             "before-upgrade",
				SourceInstanceID: "inst_1",
				State:            "ready",
				CreatedAt:        time.Unix(550, 0),
				ReadyAt:          time.Unix(560, 0),
			},
		},
		Container: &ports.ContainerInstanceStatus{
			Replicas:      3,
			ReadyReplicas: 2,
			Revision:      "rev-harbor-app-1",
			RolloutStatus: "progressing",
			History: []ports.ContainerRevisionHistory{
				{Revision: "rev-harbor-app-1", Image: "harbor/app:1", CreatedAt: time.Unix(540, 0)},
			},
		},
		GPU: &ports.GPUInstanceStatus{
			Vendor:             ports.GPUVendorNVIDIA,
			Model:              "A100",
			Count:              2,
			SchedulingReason:   "scheduled by test inventory",
			UtilizationPercent: 0,
		},
		ResourceRefs: []string{"kubernetes/Deployment/app-01"},
		Status: ports.WorkloadStatus{
			Ref: ports.WorkloadRef{
				TenantID:   "5dbb1d01-0000-4000-8000-000000000001",
				InstanceID: "inst_1",
				Kind:       ports.WorkloadKindContainer,
				ProviderID: "planning/container/tenant-a/1",
			},
			State:    ports.WorkloadStateRunning,
			Endpoint: "/instances/inst_1",
		},
	})
	if err != nil {
		t.Fatalf("UpsertStatus() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO workload_instances") {
		t.Fatalf("sql = %q, want workload_instances insert", tx.sql)
	}
	if !strings.Contains(tx.sql, "lifecycle_policy") {
		t.Fatalf("sql = %q, want lifecycle_policy persistence", tx.sql)
	}
	if !strings.Contains(tx.sql, "snapshots") {
		t.Fatalf("sql = %q, want snapshots persistence", tx.sql)
	}
	if !strings.Contains(tx.sql, "container_status") {
		t.Fatalf("sql = %q, want container status persistence", tx.sql)
	}
	if !strings.Contains(tx.sql, "gpu_status") {
		t.Fatalf("sql = %q, want gpu status persistence", tx.sql)
	}
	if got, want := tx.args[2], "app-01"; got != want {
		t.Fatalf("name arg = %v, want %s", got, want)
	}
	if got, want := tx.args[4], "vpc-a"; got != want {
		t.Fatalf("vpc arg = %v, want %s", got, want)
	}
	if got, want := tx.args[5], "subnet-a"; got != want {
		t.Fatalf("subnet arg = %v, want %s", got, want)
	}
	if got, want := tx.args[6], "10.72.0.10"; got != want {
		t.Fatalf("private ip arg = %v, want %s", got, want)
	}
	if got, want := tx.args[11], "running"; got != want {
		t.Fatalf("state arg = %v, want %s", got, want)
	}
	if got := tx.args[17]; !strings.Contains(got.(string), "TerminationProtection") {
		t.Fatalf("lifecycle arg = %v, want termination protection policy", got)
	}
	if got := tx.args[18]; !strings.Contains(got.(string), "ssh-key-a") {
		t.Fatalf("ssh arg = %v, want ssh key reference", got)
	}
	if got := tx.args[19]; !strings.Contains(got.(string), "snap-a") {
		t.Fatalf("snapshots arg = %v, want snapshot metadata", got)
	}
	if got := tx.args[20]; !strings.Contains(got.(string), "rev-harbor-app-1") {
		t.Fatalf("container arg = %v, want rollout metadata", got)
	}
	if got := tx.args[21]; !strings.Contains(got.(string), "A100") {
		t.Fatalf("gpu arg = %v, want gpu metadata", got)
	}
}

func TestMetadataInstanceStoreRejectsMissingInstanceID(t *testing.T) {
	store := NewMetadataInstanceStore(fakeMetadataStore{tx: &fakeMetadataTx{}})
	err := store.UpsertStatus(context.Background(), ports.WorkloadInstanceRecord{
		TenantID: "5dbb1d01-0000-4000-8000-000000000001",
		Name:     "app-01",
		Kind:     ports.WorkloadKindContainer,
		Status: ports.WorkloadStatus{
			State: ports.WorkloadStatePending,
		},
	})
	if err == nil {
		t.Fatalf("UpsertStatus() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "instanceID") {
		t.Fatalf("error = %q, want instanceID", err)
	}
}
