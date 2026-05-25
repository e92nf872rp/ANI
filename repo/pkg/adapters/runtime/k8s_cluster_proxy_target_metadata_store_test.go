package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kubercloud/ani/pkg/ports"
)

func TestMetadataK8sClusterProxyTargetStoreUpsertsTarget(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataK8sClusterProxyTargetStore(fakeMetadataStore{tx: tx})

	err := store.UpsertK8sClusterProxyTarget(context.Background(), ports.K8sClusterProxyTarget{
		TenantID:    networkStoreTenantID,
		ClusterID:   "k8sclu-a",
		Server:      "https://vc-a.example/",
		BearerToken: "token-a",
	})
	if err != nil {
		t.Fatalf("UpsertK8sClusterProxyTarget() error = %v", err)
	}
	if !strings.Contains(tx.sql, "INSERT INTO k8s_cluster_proxy_targets") {
		t.Fatalf("sql = %q, want k8s_cluster_proxy_targets insert", tx.sql)
	}
	if got, want := tx.args[1], "k8sclu-a"; got != want {
		t.Fatalf("cluster_id arg = %v, want %s", got, want)
	}
	if got, want := tx.args[2], "https://vc-a.example"; got != want {
		t.Fatalf("server arg = %v, want trimmed %s", got, want)
	}
	if got, want := tx.args[3], "token-a"; got != want {
		t.Fatalf("bearer_token arg = %v, want %s", got, want)
	}
}

func TestMetadataK8sClusterProxyTargetStoreResolvesTarget(t *testing.T) {
	tx := &fakeMetadataTx{
		row: fakeMetadataRow{values: []any{
			networkStoreTenantID,
			"k8sclu-a",
			"https://vc-a.example",
			"token-a",
		}},
	}
	store := NewMetadataK8sClusterProxyTargetStore(fakeMetadataStore{tx: tx})

	got, err := store.ResolveK8sClusterProxyTarget(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  networkStoreTenantID,
		ClusterID: "k8sclu-a",
	})
	if err != nil {
		t.Fatalf("ResolveK8sClusterProxyTarget() error = %v", err)
	}
	if !strings.Contains(tx.queryRowSQL, "FROM k8s_cluster_proxy_targets") {
		t.Fatalf("query sql = %q, want proxy target lookup", tx.queryRowSQL)
	}
	if got.Server != "https://vc-a.example" || got.BearerToken != "token-a" {
		t.Fatalf("resolved target = %+v", got)
	}
}

func TestMetadataK8sClusterProxyTargetStoreMapsMissingTarget(t *testing.T) {
	tx := &fakeMetadataTx{row: fakeMetadataRow{err: ports.ErrNotFound}}
	store := NewMetadataK8sClusterProxyTargetStore(fakeMetadataStore{tx: tx})

	_, err := store.ResolveK8sClusterProxyTarget(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  networkStoreTenantID,
		ClusterID: "missing",
	})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("Resolve missing target error = %v, want ErrNotFound", err)
	}
}

func TestMetadataK8sClusterProxyTargetStoreDeletesTarget(t *testing.T) {
	tx := &fakeMetadataTx{}
	store := NewMetadataK8sClusterProxyTargetStore(fakeMetadataStore{tx: tx})

	err := store.DeleteK8sClusterProxyTarget(context.Background(), ports.K8sClusterGetRequest{
		TenantID:  networkStoreTenantID,
		ClusterID: "k8sclu-a",
	})
	if err != nil {
		t.Fatalf("DeleteK8sClusterProxyTarget() error = %v", err)
	}
	if !strings.Contains(tx.sql, "DELETE FROM k8s_cluster_proxy_targets") {
		t.Fatalf("sql = %q, want proxy target delete", tx.sql)
	}
}
