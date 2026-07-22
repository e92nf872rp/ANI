package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalProviderDryRun struct {
	now func() time.Time
}

type ProviderDryRunOption func(*LocalProviderDryRun)

func WithDryRunClock(now func() time.Time) ProviderDryRunOption {
	return func(executor *LocalProviderDryRun) {
		if now != nil {
			executor.now = now
		}
	}
}

func NewLocalProviderDryRun(options ...ProviderDryRunOption) *LocalProviderDryRun {
	executor := &LocalProviderDryRun{now: time.Now}
	for _, option := range options {
		option(executor)
	}
	return executor
}

func (e *LocalProviderDryRun) DryRun(_ context.Context, manifests []ports.WorkloadManifest, admission ports.WorkloadAdmissionResult) (ports.WorkloadProviderDryRunResult, error) {
	if !admission.Allowed {
		return ports.WorkloadProviderDryRunResult{
			Accepted:      false,
			ManifestCount: len(manifests),
			Reason:        "admission denied: " + admission.Reason,
			Warnings:      admission.Warnings,
			CheckedAt:     e.now().UTC(),
		}, nil
	}
	if len(manifests) == 0 {
		return ports.WorkloadProviderDryRunResult{}, fmt.Errorf("%w: at least one manifest is required", ports.ErrInvalid)
	}

	provider := primaryProvider(manifests)
	for _, manifest := range manifests {
		if !isAuxiliarySecretManifest(manifest) && manifest.Provider != provider {
			return ports.WorkloadProviderDryRunResult{
				Accepted:      false,
				Provider:      provider,
				ManifestCount: len(manifests),
				Reason:        "mixed providers are not allowed in one dry-run batch",
				CheckedAt:     e.now().UTC(),
			}, nil
		}
		doc, err := parseManifestDocument(manifest.Content)
		if err != nil {
			return ports.WorkloadProviderDryRunResult{}, err
		}
		if err := validateProviderDryRunDocument(manifest.Provider, doc); err != nil {
			return ports.WorkloadProviderDryRunResult{
				Accepted:      false,
				Provider:      provider,
				ManifestCount: len(manifests),
				Reason:        err.Error(),
				CheckedAt:     e.now().UTC(),
			}, nil
		}
	}

	return ports.WorkloadProviderDryRunResult{
		Accepted:      true,
		Provider:      provider,
		ManifestCount: len(manifests),
		Reason:        "accepted by local provider dry-run",
		Warnings:      admission.Warnings,
		CheckedAt:     e.now().UTC(),
	}, nil
}

func parseManifestDocument(content string) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(content), &doc); err != nil {
		return nil, fmt.Errorf("%w: invalid manifest JSON: %v", ports.ErrInvalid, err)
	}
	return doc, nil
}

func validateProviderDryRunDocument(provider string, doc map[string]any) error {
	kind, _ := doc["kind"].(string)
	apiVersion, _ := doc["apiVersion"].(string)
	switch provider {
	case "kubevirt":
		if kind != "VirtualMachine" || apiVersion != "kubevirt.io/v1" {
			return fmt.Errorf("kubevirt provider requires kubevirt.io/v1 VirtualMachine")
		}
	case "kubernetes":
		switch kind {
		case "Deployment":
			if apiVersion != "apps/v1" {
				return fmt.Errorf("kubernetes Deployment requires apps/v1")
			}
		case "Job":
			if apiVersion != "batch/v1" {
				return fmt.Errorf("kubernetes Job requires batch/v1")
			}
		case "NetworkPolicy":
			if apiVersion != "networking.k8s.io/v1" {
				return fmt.Errorf("kubernetes NetworkPolicy requires networking.k8s.io/v1")
			}
		case "Service":
			if apiVersion != "v1" {
				return fmt.Errorf("kubernetes Service requires v1")
			}
		case "PersistentVolumeClaim":
			if apiVersion != "v1" {
				return fmt.Errorf("kubernetes PersistentVolumeClaim requires v1")
			}
		case "VolumeSnapshot":
			if apiVersion != "snapshot.storage.k8s.io/v1" {
				return fmt.Errorf("kubernetes VolumeSnapshot requires snapshot.storage.k8s.io/v1")
			}
		case "Secret":
			if apiVersion != "v1" {
				return fmt.Errorf("kubernetes Secret requires v1")
			}
		default:
			return fmt.Errorf("kubernetes provider does not allow kind %q", kind)
		}
	case "kubeovn":
		switch kind {
		case "Vpc", "Subnet":
			if apiVersion != "kubeovn.io/v1" {
				return fmt.Errorf("kubeovn %s requires kubeovn.io/v1", kind)
			}
		default:
			return fmt.Errorf("kubeovn provider does not allow kind %q", kind)
		}
	default:
		return fmt.Errorf("provider %q is not configured for dry-run", provider)
	}
	return nil
}

var _ ports.WorkloadProviderDryRun = (*LocalProviderDryRun)(nil)

// primaryProvider returns the provider of the first non-auxiliary manifest in
// the batch. Workload identity Secrets are auxiliary Kubernetes resources that
// may accompany a primary workload of a different provider (e.g. a kubevirt
// VirtualMachine), so they must not be used to determine the batch's primary
// provider.
func primaryProvider(manifests []ports.WorkloadManifest) string {
	for _, manifest := range manifests {
		if !isAuxiliarySecretManifest(manifest) {
			return manifest.Provider
		}
	}
	if len(manifests) > 0 {
		return manifests[0].Provider
	}
	return ""
}

// isAuxiliarySecretManifest reports whether the manifest is an auxiliary
// Kubernetes Secret rendered to back the workload identity token. Such
// Secrets always belong to the "kubernetes" provider regardless of the
// primary workload's provider, so the dry-run and apply gates skip the
// mixed-provider check for them while still validating them as kubernetes
// resources.
func isAuxiliarySecretManifest(manifest ports.WorkloadManifest) bool {
	return manifest.Kind == "Secret" && manifest.Provider == "kubernetes"
}
