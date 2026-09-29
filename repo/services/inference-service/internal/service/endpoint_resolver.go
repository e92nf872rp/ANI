package service

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/kubercloud/ani/services/inference-service/internal/domain"
	"github.com/kubercloud/ani/services/inference-service/internal/repository"
)

// InternalEndpoint is the private data-plane connection target for a ready service.
// It is deliberately separate from ServiceView, which is a tenant-facing projection.
type InternalEndpoint struct {
	TenantID        uuid.UUID
	ServiceID       uuid.UUID
	BaseURL         string
	ServedModelName string
	Task            domain.InferenceTask
	Status          domain.Status
}

// ResolveInternalEndpoint returns the private OpenAI-compatible endpoint for a service.
// The repository query is tenant-scoped; the additional identity checks keep this boundary
// safe when a non-Postgres store is used in tests or during migration.
func (c *Controller) ResolveInternalEndpoint(ctx context.Context, tenantID, serviceID uuid.UUID) (InternalEndpoint, error) {
	if tenantID == uuid.Nil || serviceID == uuid.Nil {
		return InternalEndpoint{}, fmt.Errorf("%w: tenant and service IDs are required", ErrInvalidInput)
	}
	resource, err := c.store.GetService(ctx, tenantID, serviceID)
	if err != nil {
		return InternalEndpoint{}, err
	}
	return c.internalEndpointFromResource(resource, tenantID, serviceID)
}

// ResolveInternalEndpointByServedModelName resolves the running service that
// owns the OpenAI model name inside one tenant. The model name is only a lookup
// key; the returned endpoint is still validated against the service identity.
func (c *Controller) ResolveInternalEndpointByServedModelName(ctx context.Context, tenantID uuid.UUID, servedModelName string) (InternalEndpoint, error) {
	servedModelName = strings.TrimSpace(servedModelName)
	if tenantID == uuid.Nil || servedModelName == "" {
		return InternalEndpoint{}, fmt.Errorf("%w: tenant and served model name are required", ErrInvalidInput)
	}
	var (
		resource domain.Service
		err      error
	)
	if byName, ok := c.store.(repository.ServedModelResolver); ok {
		resource, err = byName.ResolveRunningServiceByServedModelName(ctx, tenantID, servedModelName)
	} else {
		var services []domain.Service
		services, err = c.store.ListServices(ctx, tenantID)
		if err == nil {
			for _, candidate := range services {
				if candidate.ServedModelName == servedModelName {
					resource = candidate
					break
				}
			}
			if resource.ID == uuid.Nil {
				err = repository.ErrNotFound
			}
		}
	}
	if err != nil {
		return InternalEndpoint{}, err
	}
	return c.internalEndpointFromResource(resource, tenantID, resource.ID)
}

func (c *Controller) internalEndpointFromResource(resource domain.Service, tenantID, serviceID uuid.UUID) (InternalEndpoint, error) {
	if resource.TenantID != tenantID || resource.ID != serviceID || resource.DeletedAt != nil {
		return InternalEndpoint{}, repository.ErrNotFound
	}
	if resource.Status != domain.StatusRunning {
		return InternalEndpoint{}, ErrInferenceServiceNotReady
	}
	if resource.Generation != resource.ObservedGeneration {
		return InternalEndpoint{}, ErrInferenceServiceNotReady
	}
	if strings.TrimSpace(resource.RuntimeEndpoint) == "" {
		return InternalEndpoint{}, ErrRuntimeEndpointMissing
	}
	if strings.TrimSpace(resource.ServedModelName) == "" {
		return InternalEndpoint{}, fmt.Errorf("%w: served model name is empty", ErrRuntimeEndpointInvalid)
	}
	baseURL, err := normalizeInternalEndpoint(resource.RuntimeEndpoint, tenantID, serviceID)
	if err != nil {
		return InternalEndpoint{}, fmt.Errorf("%w: %v", ErrRuntimeEndpointInvalid, err)
	}
	return InternalEndpoint{
		TenantID:        tenantID,
		ServiceID:       serviceID,
		BaseURL:         baseURL,
		ServedModelName: resource.ServedModelName,
		Task:            domain.NormalizeInferenceTask(resource.DesiredSpec.ExecutionProfile.Task),
		Status:          resource.Status,
	}, nil
}

func normalizeInternalEndpoint(raw string, tenantID, serviceID uuid.UUID) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || u.RawPath != "" {
		return "", fmt.Errorf("endpoint must be a plain HTTP service URL")
	}
	if u.Port() != "8000" || strings.Contains(u.Host, "%") {
		return "", fmt.Errorf("endpoint must use the inference service port")
	}
	host := u.Hostname()
	if host == "" || host != strings.ToLower(host) || strings.HasSuffix(host, ".") || net.ParseIP(host) != nil || !validInternalDNSName(host) {
		return "", fmt.Errorf("endpoint host is not a Kubernetes service DNS name")
	}
	canonicalHost, ok := allowedRuntimeHosts(tenantID, serviceID)[host]
	if !ok {
		return "", fmt.Errorf("endpoint host is not owned by the service")
	}
	return "http://" + canonicalHost + ":8000/v1", nil
}

func allowedRuntimeHosts(tenantID, serviceID uuid.UUID) map[string]string {
	serviceName := strings.ToLower(serviceID.String())
	if serviceName[0] < 'a' || serviceName[0] > 'z' {
		serviceName = "pw-" + serviceName
	}
	namespace := "ani-tenant-" + strings.ToLower(tenantID.String()) + ".svc"
	hosts := make(map[string]string, 4)
	for _, suffix := range []string{"", "-http"} {
		short := serviceName + suffix + "." + namespace
		canonical := short + ".cluster.local"
		hosts[short] = canonical
		hosts[canonical] = canonical
	}
	return hosts
}

func validInternalDNSName(name string) bool {
	if len(name) > 253 || strings.Contains(name, "..") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
