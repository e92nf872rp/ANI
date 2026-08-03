package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/pkg/ports"
)

var storageControlPlaneRequiredTables = []string{
	"storage_volumes",
	"storage_volume_auto_snapshot_policies",
	"storage_volume_mount_events",
	"storage_volume_snapshots",
	"storage_filesystems",
	"storage_filesystem_mount_targets",
	"storage_filesystem_attachments",
	"storage_buckets",
	"storage_bucket_lifecycle_rules",
	"storage_objects",
	"vector_stores",
	"vector_store_knowledge_base_links",
}

func storageNeedsControlPlaneStore(cfg gatewayStorageRuntimeConfig) bool {
	provider := strings.TrimSpace(cfg.ProviderMode)
	objectStore := strings.TrimSpace(cfg.ObjectStoreProvider)
	switch {
	case provider == "kubernetes_rest":
		return true
	case objectStore == "minio":
		return true
	default:
		return false
	}
}

func connectStorageControlPlaneStore(ctx context.Context, databaseURL string, injected ports.MetadataStore) (ports.MetadataStore, func(), error) {
	closeStore := func() {}
	var store ports.MetadataStore
	if injected != nil {
		store = injected
	} else {
		if strings.TrimSpace(databaseURL) == "" {
			return nil, closeStore, fmt.Errorf("%w: DATABASE_URL is required for storage/vector control-plane persistence", ports.ErrNotConfigured)
		}
		connected, closeConnected, err := bootstrap.ConnectMetadataStore(ctx, databaseURL)
		if err != nil {
			return nil, closeStore, err
		}
		store = connected
		closeStore = closeConnected
	}
	if err := validateStorageControlPlaneSchema(ctx, store); err != nil {
		closeStore()
		return nil, func() {}, err
	}
	return store, closeStore, nil
}

func validateStorageControlPlaneSchema(ctx context.Context, store ports.MetadataStore) error {
	if store == nil {
		return fmt.Errorf("%w: metadata store is required", ports.ErrNotConfigured)
	}
	if err := store.Ping(ctx); err != nil {
		return fmt.Errorf("%w: storage control-plane database unreachable: %v", ports.ErrUnavailable, err)
	}
	return store.WithPlatformTx(ctx, func(ctx context.Context, tx ports.MetadataTx) error {
		for _, table := range storageControlPlaneRequiredTables {
			var reg *string
			if err := tx.QueryRow(ctx, `SELECT to_regclass($1)::text`, "public."+table).Scan(&reg); err != nil {
				return fmt.Errorf("%w: inspect storage control-plane table %s: %v", ports.ErrUnavailable, table, err)
			}
			if reg == nil || strings.TrimSpace(*reg) == "" {
				return fmt.Errorf("%w: storage control-plane schema incomplete: missing table %s", ports.ErrNotConfigured, table)
			}
		}
		return nil
	})
}
