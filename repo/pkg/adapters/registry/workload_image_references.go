package registry

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

// WorkloadImageReferenceReader reads ANI's persisted workload records rather
// than asking Harbor, which cannot know whether a tag is used by an ANI workload.
type WorkloadImageReferenceReader struct{ instances ports.WorkloadInstanceStore }

func NewWorkloadImageReferenceReader(instances ports.WorkloadInstanceStore) *WorkloadImageReferenceReader {
	return &WorkloadImageReferenceReader{instances: instances}
}

func (r *WorkloadImageReferenceReader) ListRegistryImageReferences(ctx context.Context, request ports.RegistryImageReferenceListRequest) (ports.RegistryImageReferenceListResult, error) {
	if err := validateRegistryTagRequest(request.TenantID, request.Project, request.Repository, request.Tag); err != nil {
		return ports.RegistryImageReferenceListResult{}, err
	}
	if r.instances == nil {
		return ports.RegistryImageReferenceListResult{}, ports.ErrNotConfigured
	}
	records, err := r.instances.List(ctx, request.TenantID, "")
	if err != nil {
		return ports.RegistryImageReferenceListResult{}, fmt.Errorf("read workload image references: %w", err)
	}
	image := request.Project + "/" + request.Repository + ":" + request.Tag
	result := ports.RegistryImageReferenceListResult{Project: request.Project, Repository: request.Repository, Tag: request.Tag, Image: image, DevProfile: harborDevProfile()}
	for _, record := range records {
		if record.Status.State == ports.WorkloadStateDeleted || !workloadUsesImage(record, image) {
			continue
		}
		result.Items = append(result.Items, ports.RegistryImageReference{Kind: string(record.Kind) + "_instance", ID: record.InstanceID, Name: record.Name, Route: "/instances/" + record.InstanceID, State: string(record.Status.State), DevProfile: harborDevProfile()})
	}
	result.DeleteBlocked = len(result.Items) > 0
	return result, nil
}

func workloadUsesImage(record ports.WorkloadInstanceRecord, expected string) bool {
	if record.Container == nil {
		return false
	}
	for _, revision := range record.Container.History {
		if normalizeRegistryImage(revision.Image) == expected {
			return true
		}
	}
	return false
}

func normalizeRegistryImage(image string) string {
	value := strings.TrimSpace(image)
	parts := strings.Split(value, "/")
	if len(parts) >= 3 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") || parts[0] == "localhost") {
		return strings.Join(parts[1:], "/")
	}
	return value
}

var _ ports.RegistryImageReferenceReader = (*WorkloadImageReferenceReader)(nil)
