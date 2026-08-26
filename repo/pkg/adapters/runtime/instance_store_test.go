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
		TenantID:    "5dbb1d01-0000-4000-8000-000000000001",
		InstanceID:  "inst_1",
		Name:        "app-01",
		Description: "approved instance summary",
		Labels:      map[string]string{"team": "platform"},
		Kind:        ports.WorkloadKindContainer,
		Provider:    "kubernetes",
		AuditID:     "5dbb1d01-0000-4000-8000-000000000002",
		Image: ports.InstanceImageSummary{
			ID:     "image-a",
			Ref:    "harbor/app@sha256:abc",
			Digest: "sha256:abc",
		},
		Compute: ports.InstanceComputeSummary{
			CPU:    "4",
			Memory: "16Gi",
			SpecID: "gpu-spec-a",
		},
		Network: ports.InstanceNetworkSummary{
			VPCID:     "vpc-a",
			SubnetID:  "subnet-a",
			PrivateIP: "10.20.0.8",
		},
		Access: ports.InstanceAccessSummary{
			ExecAvailable: true,
		},
		StorageAttachments: []ports.WorkloadStorageAttachment{
			{
				ResourceType: "volume",
				ResourceID:   "volume-a",
				Status:       "attached",
				TaskID:       "task-a",
			},
		},
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
	for _, column := range []string{
		"description",
		"labels",
		"image_summary",
		"compute_summary",
		"network_summary",
		"access_summary",
		"storage_attachments",
		"sandbox_status",
	} {
		if !strings.Contains(tx.sql, column) {
			t.Fatalf("sql = %q, want %s persistence", tx.sql, column)
		}
	}
	if got, want := tx.args[2], "app-01"; got != want {
		t.Fatalf("name arg = %v, want %s", got, want)
	}
	if got, want := tx.args[8], "running"; got != want {
		t.Fatalf("state arg = %v, want %s", got, want)
	}
	if got := tx.args[14]; !strings.Contains(got.(string), "TerminationProtection") {
		t.Fatalf("lifecycle arg = %v, want termination protection policy", got)
	}
	if got := tx.args[15]; !strings.Contains(got.(string), "ssh-key-a") {
		t.Fatalf("ssh arg = %v, want ssh key reference", got)
	}
	if got := tx.args[16]; !strings.Contains(got.(string), "snap-a") {
		t.Fatalf("snapshots arg = %v, want snapshot metadata", got)
	}
	if got := tx.args[17]; !strings.Contains(got.(string), "rev-harbor-app-1") {
		t.Fatalf("container arg = %v, want rollout metadata", got)
	}
	if got := tx.args[18]; !strings.Contains(got.(string), "A100") {
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

func TestMetadataInstanceStoreNeverSelectsDeletingOrDeletedReconcileTargets(t *testing.T) {
	tx := &fakeMetadataTx{rows: emptyMetadataRows{}}
	store := NewMetadataInstanceStore(fakeMetadataStore{tx: tx})

	if _, err := store.ListReconcileTargets(context.Background(), ports.ReconcileTargetListRequest{}); err != nil {
		t.Fatalf("ListReconcileTargets() error = %v", err)
	}
	if !strings.Contains(tx.querySQL, "state NOT IN ('deleting', 'deleted')") {
		t.Fatalf("query = %q, want deleting/deleted exclusion", tx.querySQL)
	}
}

func TestUpsertStatusTx(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataInstanceStore(fakeMetadataStore{tx: tx}, WithInstanceStoreClock(func() time.Time {
		return time.Unix(700, 0)
	}))
	record := ports.WorkloadInstanceRecord{
		TenantID:   "5dbb1d01-0000-4000-8000-000000000001",
		InstanceID: "inst_tx_1",
		Name:       "quota-app",
		Kind:       ports.WorkloadKindGPUContainer,
		Provider:   "kubernetes",
		AuditID:    "audit-tx-1",
		QuotaTxIDs: []string{"tx_001", "tx_002"},
		Status: ports.WorkloadStatus{
			State: ports.WorkloadStatePending,
			Ref: ports.WorkloadRef{
				TenantID:   "5dbb1d01-0000-4000-8000-000000000001",
				InstanceID: "inst_tx_1",
				Kind:       ports.WorkloadKindGPUContainer,
			},
		},
		CreatedAt: time.Unix(700, 0),
		UpdatedAt: time.Unix(700, 0),
	}
	err := store.UpsertStatusTx(context.Background(), tx, record)
	if err != nil {
		t.Fatalf("UpsertStatusTx() error = %v", err)
	}
	if len(tx.execs) == 0 {
		t.Fatalf("no SQL executed on tx")
	}
	// quota_tx_ids must be written in the same transaction.
	if !strings.Contains(tx.sql, "quota_tx_ids") {
		t.Fatalf("SQL = %q, want quota_tx_ids column", tx.sql)
	}
	// Find the quota_tx_ids argument (JSONB array passed as string).
	foundQuotaTxIDs := false
	for _, arg := range tx.args {
		if s, ok := arg.(string); ok && strings.Contains(s, "tx_001") {
			foundQuotaTxIDs = true
			break
		}
	}
	if !foundQuotaTxIDs {
		t.Fatalf("args = %v, want quota_tx_ids JSON containing tx_001", tx.args)
	}
}

func TestUpsertStatusTxRejectsNilTx(t *testing.T) {
	store := NewMetadataInstanceStore(fakeMetadataStore{tx: &fakeMetadataTx{}})
	err := store.UpsertStatusTx(context.Background(), nil, ports.WorkloadInstanceRecord{
		TenantID:   "t",
		InstanceID: "i",
		Name:       "n",
		Kind:       ports.WorkloadKindContainer,
		Status:     ports.WorkloadStatus{State: ports.WorkloadStatePending},
	})
	if err == nil {
		t.Fatalf("UpsertStatusTx(nil tx) error = nil, want error")
	}
}

func TestUpsertStatusTxRejectsInvalidRecord(t *testing.T) {
	store := NewMetadataInstanceStore(fakeMetadataStore{tx: &fakeMetadataTx{}})
	err := store.UpsertStatusTx(context.Background(), &fakeMetadataTx{}, ports.WorkloadInstanceRecord{
		TenantID: "",
	})
	if err == nil {
		t.Fatalf("UpsertStatusTx(invalid record) error = nil, want error")
	}
}

type emptyMetadataRows struct{}

func (emptyMetadataRows) Next() bool        { return false }
func (emptyMetadataRows) Scan(...any) error { return nil }
func (emptyMetadataRows) Err() error        { return nil }
func (emptyMetadataRows) Close()            {}
