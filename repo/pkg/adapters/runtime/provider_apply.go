package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalProviderApply struct {
	enabled bool
	now     func() time.Time
}

type ProviderApplyOption func(*LocalProviderApply)

func WithProviderApplyEnabled(enabled bool) ProviderApplyOption {
	return func(executor *LocalProviderApply) {
		executor.enabled = enabled
	}
}

func WithApplyClock(now func() time.Time) ProviderApplyOption {
	return func(executor *LocalProviderApply) {
		if now != nil {
			executor.now = now
		}
	}
}

func NewLocalProviderApply(options ...ProviderApplyOption) *LocalProviderApply {
	executor := &LocalProviderApply{now: time.Now}
	for _, option := range options {
		option(executor)
	}
	return executor
}

func (e *LocalProviderApply) Apply(_ context.Context, request ports.WorkloadProviderApplyRequest) (ports.WorkloadProviderApplyResult, error) {
	if !e.enabled {
		return ports.WorkloadProviderApplyResult{
			Applied:       false,
			Provider:      request.DryRunResult.Provider,
			ManifestCount: len(request.Manifests),
			Operation:     request.Operation,
			Reason:        "provider apply is disabled by execution switch",
			Warnings:      request.DryRunResult.Warnings,
			AppliedAt:     e.now().UTC(),
		}, nil
	}
	if err := validateProviderApplyRequest(request); err != nil {
		return ports.WorkloadProviderApplyResult{}, err
	}

	provider := primaryProvider(request.Manifests)
	refs := make([]string, 0, len(request.Manifests))
	for _, manifest := range request.Manifests {
		refs = append(refs, manifest.Provider+"/"+manifest.Kind+"/"+manifest.Name)
	}

	return ports.WorkloadProviderApplyResult{
		Applied:       true,
		Provider:      provider,
		ManifestCount: len(request.Manifests),
		Operation:     request.Operation,
		ResourceRefs:  refs,
		Reason:        "accepted by local provider apply gate",
		Warnings:      request.DryRunResult.Warnings,
		AppliedAt:     e.now().UTC(),
	}, nil
}

func validateProviderApplyRequest(request ports.WorkloadProviderApplyRequest) error {
	if request.TenantID == "" {
		return fmt.Errorf("%w: tenant id is required for provider apply", ports.ErrInvalid)
	}
	if request.UserID == "" {
		return fmt.Errorf("%w: user id is required for provider apply", ports.ErrInvalid)
	}
	if request.InstanceID == "" {
		return fmt.Errorf("%w: instance id is required for provider apply", ports.ErrInvalid)
	}
	if request.AuditID == "" {
		return fmt.Errorf("%w: audit id is required before provider apply", ports.ErrInvalid)
	}
	if request.PermissionProof == "" {
		return fmt.Errorf("%w: permission proof is required before provider apply", ports.ErrInvalid)
	}
	if request.Operation != ports.WorkloadLifecycleCreate {
		return fmt.Errorf("%w: provider apply gate currently allows create only", ports.ErrInvalid)
	}
	if !request.AdmissionResult.Allowed {
		return fmt.Errorf("%w: admission must be allowed before provider apply", ports.ErrInvalid)
	}
	if !request.DryRunResult.Accepted {
		return fmt.Errorf("%w: provider dry-run must be accepted before provider apply", ports.ErrInvalid)
	}
	if len(request.Manifests) == 0 {
		return fmt.Errorf("%w: at least one manifest is required", ports.ErrInvalid)
	}

	provider := primaryProvider(request.Manifests)
	if provider == "" {
		return fmt.Errorf("%w: manifest provider is required", ports.ErrInvalid)
	}
	if request.DryRunResult.Provider != "" && request.DryRunResult.Provider != provider {
		return fmt.Errorf("%w: dry-run provider does not match manifest provider", ports.ErrInvalid)
	}
	if request.DryRunResult.ManifestCount != 0 && request.DryRunResult.ManifestCount != len(request.Manifests) {
		return fmt.Errorf("%w: dry-run manifest count does not match apply request", ports.ErrInvalid)
	}
	for _, manifest := range request.Manifests {
		if !isAuxiliarySecretManifest(manifest) && manifest.Provider != provider {
			return fmt.Errorf("%w: mixed providers are not allowed in one apply batch", ports.ErrInvalid)
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(manifest.Content), &doc); err != nil {
			return fmt.Errorf("%w: invalid manifest JSON: %v", ports.ErrInvalid, err)
		}
		if err := validateProviderDryRunDocument(manifest.Provider, doc); err != nil {
			return err
		}
	}
	return nil
}

var _ ports.WorkloadProviderApply = (*LocalProviderApply)(nil)
