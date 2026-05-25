package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestLocalK8sClusterProxyTargetStoreResolvesPerTenantCluster(t *testing.T) {
	store := NewLocalK8sClusterProxyTargetStore()
	target := ports.K8sClusterProxyTarget{
		TenantID:    "tenant-a",
		ClusterID:   "k8sclu-a",
		Server:      "https://vc-a.example",
		BearerToken: "token-a",
	}
	if err := store.UpsertK8sClusterProxyTarget(context.Background(), target); err != nil {
		t.Fatalf("UpsertK8sClusterProxyTarget() error = %v", err)
	}

	got, err := store.ResolveK8sClusterProxyTarget(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-a",
	})
	if err != nil {
		t.Fatalf("ResolveK8sClusterProxyTarget() error = %v", err)
	}
	if got.Server != target.Server || got.BearerToken != target.BearerToken {
		t.Fatalf("resolved target = %+v, want %+v", got, target)
	}

	target.BearerToken = "mutated-after-upsert"
	got, err = store.ResolveK8sClusterProxyTarget(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-a",
	})
	if err != nil {
		t.Fatalf("ResolveK8sClusterProxyTarget() after mutation error = %v", err)
	}
	if got.BearerToken != "token-a" {
		t.Fatalf("resolved token = %q, want stored copy token-a", got.BearerToken)
	}
}

func TestLocalK8sClusterProxyTargetStoreKeepsTenantIsolation(t *testing.T) {
	store := NewLocalK8sClusterProxyTargetStore()
	if err := store.UpsertK8sClusterProxyTarget(context.Background(), ports.K8sClusterProxyTarget{
		TenantID:    "tenant-a",
		ClusterID:   "shared-cluster-id",
		Server:      "https://tenant-a.example",
		BearerToken: "token-a",
	}); err != nil {
		t.Fatalf("Upsert tenant-a target: %v", err)
	}
	if err := store.UpsertK8sClusterProxyTarget(context.Background(), ports.K8sClusterProxyTarget{
		TenantID:    "tenant-b",
		ClusterID:   "shared-cluster-id",
		Server:      "https://tenant-b.example",
		BearerToken: "token-b",
	}); err != nil {
		t.Fatalf("Upsert tenant-b target: %v", err)
	}

	got, err := store.ResolveK8sClusterProxyTarget(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  "tenant-b",
		ClusterID: "shared-cluster-id",
	})
	if err != nil {
		t.Fatalf("Resolve tenant-b target: %v", err)
	}
	if got.Server != "https://tenant-b.example" || got.BearerToken != "token-b" {
		t.Fatalf("tenant-b resolved target = %+v", got)
	}
}

func TestLocalK8sClusterProxyTargetStoreDeletesTarget(t *testing.T) {
	store := NewLocalK8sClusterProxyTargetStore()
	req := ports.K8sClusterGetRequest{TenantID: "tenant-a", ClusterID: "k8sclu-a"}
	if err := store.UpsertK8sClusterProxyTarget(context.Background(), ports.K8sClusterProxyTarget{
		TenantID:  req.TenantID,
		ClusterID: req.ClusterID,
		Server:    "https://vc-a.example",
	}); err != nil {
		t.Fatalf("Upsert target: %v", err)
	}
	if err := store.DeleteK8sClusterProxyTarget(context.Background(), req); err != nil {
		t.Fatalf("DeleteK8sClusterProxyTarget() error = %v", err)
	}
	if _, err := store.ResolveK8sClusterProxyTarget(context.Background(), req); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Resolve after delete error = %v, want ErrNotFound", err)
	}
}

func TestLocalK8sClusterProxyTargetStoreValidatesTarget(t *testing.T) {
	store := NewLocalK8sClusterProxyTargetStore()
	if err := store.UpsertK8sClusterProxyTarget(context.Background(), ports.K8sClusterProxyTarget{
		TenantID:  "tenant-a",
		ClusterID: "k8sclu-a",
	}); !errors.Is(err, ports.ErrInvalid) {
		t.Fatalf("Upsert without server error = %v, want ErrInvalid", err)
	}
}
