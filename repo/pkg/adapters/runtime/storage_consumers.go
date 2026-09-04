package runtime

import (
	"context"

	"github.com/kubercloud/ani/pkg/ports"
)

// StorageConsumer describes an active instance that references a storage
// resource (volume/filesystem) through its storage attachments.
type StorageConsumer struct {
	InstanceID   string
	InstanceName string
	Kind         ports.WorkloadKind
	State        ports.WorkloadState
	MountPath    string
}

// storageActiveStates lists the instance states that hold storage
// attachments. provisioning/running are the primary occupancy states;
// pending/starting cover instances about to deploy or restarting, and
// stopping keeps the window before replicas reach 0 (the volume attach is
// still present). stopped/failed/deleted are released or terminal.
var storageActiveStates = map[ports.WorkloadState]struct{}{
	ports.WorkloadStatePending:      {},
	ports.WorkloadStateProvisioning: {},
	ports.WorkloadStateStarting:     {},
	ports.WorkloadStateRunning:      {},
	ports.WorkloadStateStopping:     {},
}

func storageStateActive(state ports.WorkloadState) bool {
	_, ok := storageActiveStates[state]
	return ok
}

// storageConsumerKinds enumerates every distinct workload kind that can carry
// storage attachments. WorkloadKindAgentSandbox aliases WorkloadKindSandbox
// so it is intentionally absent.
var storageConsumerKinds = []ports.WorkloadKind{
	ports.WorkloadKindVM,
	ports.WorkloadKindContainer,
	ports.WorkloadKindGPUContainer,
	ports.WorkloadKindInference,
	ports.WorkloadKindNotebook,
	ports.WorkloadKindSandbox,
	ports.WorkloadKindBatchJob,
}

func storageConsumerKey(resourceType, resourceID string) string {
	return resourceType + "/" + resourceID
}

// storageConsumersFromRecord extracts the consumer entries of one record for
// the given resource, deduplicating by instance so multiple mount paths do
// not produce duplicate used_by rows (the first mount path wins).
func storageConsumersFromRecord(record ports.WorkloadInstanceRecord, resourceType, resourceID string) []StorageConsumer {
	if !storageStateActive(record.Status.State) {
		return nil
	}
	for _, attachment := range record.StorageAttachments {
		if attachment.ResourceType != resourceType || attachment.ResourceID != resourceID {
			continue
		}
		return []StorageConsumer{{
			InstanceID:   record.InstanceID,
			InstanceName: record.Name,
			Kind:         record.Kind,
			State:        record.Status.State,
			MountPath:    attachment.MountPath,
		}}
	}
	return nil
}

// ListStorageConsumers returns active tenant instances whose storage
// attachments reference resourceType/resourceID. Single-resource form used by
// detail handlers and future admission prechecks.
func ListStorageConsumers(ctx context.Context, store ports.WorkloadInstanceStore, tenantID, resourceType, resourceID string) ([]StorageConsumer, error) {
	if store == nil || resourceID == "" {
		return nil, nil
	}
	consumers := make([]StorageConsumer, 0)
	for _, kind := range storageConsumerKinds {
		records, err := store.List(ctx, tenantID, kind)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			consumers = append(consumers, storageConsumersFromRecord(record, resourceType, resourceID)...)
		}
	}
	return consumers, nil
}

// ListAllStorageConsumers indexes the storage consumers of every active
// instance of the tenant, keyed by "resourceType/resourceID" (e.g.
// "volume/vol_x", "filesystem/fs_x"). Batch form used by list handlers so a
// whole response page needs one store scan instead of N+1 lookups.
func ListAllStorageConsumers(ctx context.Context, store ports.WorkloadInstanceStore, tenantID string) (map[string][]StorageConsumer, error) {
	if store == nil {
		return nil, nil
	}
	index := make(map[string][]StorageConsumer)
	for _, kind := range storageConsumerKinds {
		records, err := store.List(ctx, tenantID, kind)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if !storageStateActive(record.Status.State) {
				continue
			}
			seen := map[string]struct{}{}
			for _, attachment := range record.StorageAttachments {
				if attachment.ResourceType == "" || attachment.ResourceID == "" {
					continue
				}
				key := storageConsumerKey(attachment.ResourceType, attachment.ResourceID)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				index[key] = append(index[key], StorageConsumer{
					InstanceID:   record.InstanceID,
					InstanceName: record.Name,
					Kind:         record.Kind,
					State:        record.Status.State,
					MountPath:    attachment.MountPath,
				})
			}
		}
	}
	return index, nil
}
