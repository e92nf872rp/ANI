package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type KubernetesProviderClient interface {
	ServerSideDryRun(ctx context.Context, manifests []ports.WorkloadManifest) (ports.WorkloadProviderDryRunResult, error)
	Apply(ctx context.Context, request ports.WorkloadProviderApplyRequest) (ports.WorkloadProviderApplyResult, error)
	Observe(ctx context.Context, request ports.WorkloadProviderStatusRequest) (ports.WorkloadProviderObservation, error)
}

type KubernetesProviderAdapter struct {
	client       KubernetesProviderClient
	applyEnabled bool
	now          func() time.Time
}

type KubernetesProviderOption func(*KubernetesProviderAdapter)

func WithKubernetesProviderApplyEnabled(enabled bool) KubernetesProviderOption {
	return func(adapter *KubernetesProviderAdapter) {
		adapter.applyEnabled = enabled
	}
}

func WithKubernetesProviderClock(now func() time.Time) KubernetesProviderOption {
	return func(adapter *KubernetesProviderAdapter) {
		if now != nil {
			adapter.now = now
		}
	}
}

func NewKubernetesProviderAdapter(client KubernetesProviderClient, options ...KubernetesProviderOption) *KubernetesProviderAdapter {
	adapter := &KubernetesProviderAdapter{
		client: client,
		now:    time.Now,
	}
	for _, option := range options {
		option(adapter)
	}
	return adapter
}

func (a *KubernetesProviderAdapter) DryRun(ctx context.Context, manifests []ports.WorkloadManifest, admission ports.WorkloadAdmissionResult) (ports.WorkloadProviderDryRunResult, error) {
	if !admission.Allowed {
		return ports.WorkloadProviderDryRunResult{
			Accepted:      false,
			ManifestCount: len(manifests),
			Reason:        "admission denied: " + admission.Reason,
			Warnings:      admission.Warnings,
			CheckedAt:     a.now().UTC(),
		}, nil
	}
	if a.client == nil {
		return ports.WorkloadProviderDryRunResult{}, ports.ErrNotConfigured
	}
	if err := validateProviderManifestBatch(manifests); err != nil {
		return ports.WorkloadProviderDryRunResult{}, err
	}
	result, err := a.client.ServerSideDryRun(ctx, manifests)
	if err != nil {
		return ports.WorkloadProviderDryRunResult{}, err
	}
	result.Warnings = append(result.Warnings, admission.Warnings...)
	if result.CheckedAt.IsZero() {
		result.CheckedAt = a.now().UTC()
	}
	return result, nil
}

func (a *KubernetesProviderAdapter) Apply(ctx context.Context, request ports.WorkloadProviderApplyRequest) (ports.WorkloadProviderApplyResult, error) {
	if !a.applyEnabled {
		return ports.WorkloadProviderApplyResult{
			Applied:       false,
			Provider:      request.DryRunResult.Provider,
			ManifestCount: len(request.Manifests),
			Operation:     request.Operation,
			Reason:        "kubernetes provider apply is disabled by execution switch",
			Warnings:      request.DryRunResult.Warnings,
			AppliedAt:     a.now().UTC(),
		}, nil
	}
	if a.client == nil {
		return ports.WorkloadProviderApplyResult{}, ports.ErrNotConfigured
	}
	if err := validateProviderApplyRequest(request); err != nil {
		return ports.WorkloadProviderApplyResult{}, err
	}
	result, err := a.client.Apply(ctx, request)
	if err != nil {
		return ports.WorkloadProviderApplyResult{}, err
	}
	if result.Provider == "" {
		result.Provider = request.Manifests[0].Provider
	}
	if result.ManifestCount == 0 {
		result.ManifestCount = len(request.Manifests)
	}
	if result.Operation == "" {
		result.Operation = request.Operation
	}
	if result.AppliedAt.IsZero() {
		result.AppliedAt = a.now().UTC()
	}
	if result.Applied && len(result.ResourceRefs) == 0 {
		return ports.WorkloadProviderApplyResult{}, fmt.Errorf("%w: applied provider result must include resource refs", ports.ErrInvalid)
	}
	return result, nil
}

func (a *KubernetesProviderAdapter) Observe(ctx context.Context, request ports.WorkloadProviderStatusRequest) (ports.WorkloadProviderObservation, error) {
	if a.client == nil {
		return ports.WorkloadProviderObservation{}, ports.ErrNotConfigured
	}
	if request.TenantID == "" || request.InstanceID == "" || request.Kind == "" {
		return ports.WorkloadProviderObservation{}, fmt.Errorf("%w: tenant id, instance id, and workload kind are required for provider observation", ports.ErrInvalid)
	}
	if !request.ApplyResult.Applied {
		return ports.WorkloadProviderObservation{}, fmt.Errorf("%w: provider apply must be applied before provider observation", ports.ErrInvalid)
	}
	observation, err := a.client.Observe(ctx, request)
	if err != nil {
		return ports.WorkloadProviderObservation{}, err
	}
	if observation.TenantID != request.TenantID || observation.InstanceID != request.InstanceID {
		return ports.WorkloadProviderObservation{}, fmt.Errorf("%w: provider observation identity does not match request", ports.ErrInvalid)
	}
	if observation.Provider == "" {
		observation.Provider = request.ApplyResult.Provider
	}
	if len(observation.ResourceRefs) == 0 {
		return ports.WorkloadProviderObservation{}, fmt.Errorf("%w: provider observation must include resource refs", ports.ErrInvalid)
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = a.now().UTC()
	}
	return observation, nil
}

func validateProviderManifestBatch(manifests []ports.WorkloadManifest) error {
	if len(manifests) == 0 {
		return fmt.Errorf("%w: at least one manifest is required", ports.ErrInvalid)
	}
	provider := primaryProvider(manifests)
	for _, manifest := range manifests {
		if !isAuxiliaryKubernetesManifest(manifest) && manifest.Provider != provider {
			return fmt.Errorf("%w: mixed providers are not allowed in one provider batch", ports.ErrInvalid)
		}
		doc, err := parseManifestDocument(manifest.Content)
		if err != nil {
			return err
		}
		if err := validateProviderDryRunDocument(manifest.Provider, doc); err != nil {
			return err
		}
	}
	return nil
}

var _ ports.WorkloadProviderDryRun = (*KubernetesProviderAdapter)(nil)
var _ ports.WorkloadProviderApply = (*KubernetesProviderAdapter)(nil)
var _ ports.WorkloadProviderStatusReader = (*KubernetesProviderAdapter)(nil)
