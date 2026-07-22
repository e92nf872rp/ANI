package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kubercloud/ani/pkg/ports"
)

type LocalAdmissionGuard struct {
	allowedKinds map[string]bool
}

func NewLocalAdmissionGuard() *LocalAdmissionGuard {
	return &LocalAdmissionGuard{
		allowedKinds: map[string]bool{
			"VirtualMachine": true,
			"Deployment":     true,
			"Job":            true,
			"Secret":         true,
		},
	}
}

func (g *LocalAdmissionGuard) Review(_ context.Context, manifests []ports.WorkloadManifest) (ports.WorkloadAdmissionResult, error) {
	if len(manifests) == 0 {
		return ports.WorkloadAdmissionResult{}, fmt.Errorf("%w: at least one manifest is required", ports.ErrInvalid)
	}

	var warnings []string
	for _, manifest := range manifests {
		if strings.TrimSpace(manifest.Content) == "" {
			return denied("empty manifest content"), nil
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(manifest.Content), &doc); err != nil {
			return ports.WorkloadAdmissionResult{}, fmt.Errorf("%w: invalid manifest JSON: %v", ports.ErrInvalid, err)
		}
		if err := g.reviewDocument(doc); err != nil {
			return denied(err.Error()), nil
		}
		if manifest.Provider == "" {
			warnings = append(warnings, "manifest provider is empty")
		}
	}

	return ports.WorkloadAdmissionResult{
		Allowed:  true,
		Reason:   "accepted by local admission guard",
		Warnings: warnings,
	}, nil
}

func (g *LocalAdmissionGuard) reviewDocument(doc map[string]any) error {
	kind, _ := doc["kind"].(string)
	if !g.allowedKinds[kind] {
		return fmt.Errorf("kind %q is not allowed", kind)
	}

	metadata, ok := doc["metadata"].(map[string]any)
	if !ok {
		return fmt.Errorf("metadata is required")
	}
	labels := stringMap(metadata["labels"])
	annotations := stringMap(metadata["annotations"])
	if labels["ani.kubercloud.io/tenant-id"] == "" {
		return fmt.Errorf("tenant label is required")
	}
	if labels["ani.kubercloud.io/instance"] == "" {
		return fmt.Errorf("instance label is required")
	}
	if annotations["ani.kubercloud.io/render-mode"] != "dry-run" {
		return fmt.Errorf("render-mode=dry-run annotation is required")
	}
	if annotations["ani.kubercloud.io/network-planes"] == "" {
		return fmt.Errorf("network plane annotation is required")
	}

	if containsHostNetwork(doc) {
		return fmt.Errorf("hostNetwork is not allowed")
	}
	if containsPrivilegedContainer(doc) {
		return fmt.Errorf("privileged containers are not allowed")
	}
	return nil
}

func denied(reason string) ports.WorkloadAdmissionResult {
	return ports.WorkloadAdmissionResult{
		Allowed: false,
		Reason:  reason,
	}
}

func stringMap(value any) map[string]string {
	result := map[string]string{}
	raw, ok := value.(map[string]any)
	if !ok {
		return result
	}
	for key, value := range raw {
		if text, ok := value.(string); ok {
			result[key] = text
		}
	}
	return result
}

func containsHostNetwork(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "hostNetwork" {
				if enabled, ok := item.(bool); ok && enabled {
					return true
				}
			}
			if containsHostNetwork(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsHostNetwork(item) {
				return true
			}
		}
	}
	return false
}

func containsPrivilegedContainer(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if key == "privileged" {
				if enabled, ok := item.(bool); ok && enabled {
					return true
				}
			}
			if containsPrivilegedContainer(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsPrivilegedContainer(item) {
				return true
			}
		}
	}
	return false
}

var _ ports.WorkloadAdmission = (*LocalAdmissionGuard)(nil)
