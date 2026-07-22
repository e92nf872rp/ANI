package registry

import (
	"context"

	"github.com/kubercloud/ani/pkg/ports"
)

type NotConfigured struct{}

var _ ports.ImageRegistry = NotConfigured{}

func (NotConfigured) EnsureProject(context.Context, string) error {
	return ports.ErrNotConfigured
}

func (NotConfigured) ListTags(context.Context, string) ([]ports.ImageTag, error) {
	return nil, ports.ErrNotConfigured
}

func (NotConfigured) GetScanStatus(context.Context, ports.ImageRef) (ports.ImageScanStatus, error) {
	return ports.ImageScanStatus{}, ports.ErrNotConfigured
}

func (NotConfigured) CreateProject(context.Context, ports.RegistryProjectRequest) (ports.RegistryProject, error) {
	return ports.RegistryProject{}, ports.ErrNotConfigured
}

func (NotConfigured) ListProjects(context.Context, ports.RegistryProjectListRequest) (ports.RegistryProjectListResult, error) {
	return ports.RegistryProjectListResult{}, ports.ErrNotConfigured
}

func (NotConfigured) ListRepositories(context.Context, ports.RegistryRepositoryListRequest) (ports.RegistryRepositoryListResult, error) {
	return ports.RegistryRepositoryListResult{}, ports.ErrNotConfigured
}

func (NotConfigured) ListArtifacts(context.Context, ports.RegistryArtifactListRequest) (ports.RegistryArtifactListResult, error) {
	return ports.RegistryArtifactListResult{}, ports.ErrNotConfigured
}

func (NotConfigured) SetRepositoryPermission(context.Context, ports.RegistryPermissionRequest) (ports.RegistryPermission, error) {
	return ports.RegistryPermission{}, ports.ErrNotConfigured
}

func (NotConfigured) GetScanResult(context.Context, ports.RegistryScanResultRequest) (ports.RegistryScanResult, error) {
	return ports.RegistryScanResult{}, ports.ErrNotConfigured
}

func (NotConfigured) CreatePullSecret(context.Context, ports.RegistryPullSecretRequest) (ports.RegistryPullSecret, error) {
	return ports.RegistryPullSecret{}, ports.ErrNotConfigured
}

func (NotConfigured) GetProjectScanReport(context.Context, ports.RegistryProjectScanReportRequest) (ports.RegistryProjectScanReport, error) {
	return ports.RegistryProjectScanReport{}, ports.ErrNotConfigured
}

func (NotConfigured) GetOverview(context.Context, ports.RegistryOverviewRequest) (ports.RegistryOverview, error) {
	return ports.RegistryOverview{}, ports.ErrNotConfigured
}

func (NotConfigured) ListImages(context.Context, ports.RegistryImageListRequest) (ports.RegistryImageListResult, error) {
	return ports.RegistryImageListResult{}, ports.ErrNotConfigured
}

func (NotConfigured) GetPushInstructions(context.Context, ports.RegistryPushInstructionsRequest) (ports.RegistryPushInstructions, error) {
	return ports.RegistryPushInstructions{}, ports.ErrNotConfigured
}

func (NotConfigured) DeleteTag(context.Context, ports.RegistryTagDeleteRequest) (ports.RegistryDeletedTag, error) {
	return ports.RegistryDeletedTag{}, ports.ErrNotConfigured
}

func (NotConfigured) ListTagReferences(context.Context, ports.RegistryImageReferenceListRequest) (ports.RegistryImageReferenceListResult, error) {
	return ports.RegistryImageReferenceListResult{}, ports.ErrNotConfigured
}
