package gpu

import (
	"context"

	"github.com/kubercloud/ani/pkg/ports"
)

type NotConfigured struct{}

func (NotConfigured) ListNodeClasses(context.Context, ports.GPUDiscoveryFilter) ([]ports.GPUNodeClass, error) {
	return nil, ports.ErrNotConfigured
}

func (NotConfigured) GetNodeClass(context.Context, string) (ports.GPUNodeClass, error) {
	return ports.GPUNodeClass{}, ports.ErrNotConfigured
}

func (NotConfigured) PlanScheduling(context.Context, ports.GPUSchedulingRequest) (ports.GPUSchedulingDecision, error) {
	return ports.GPUSchedulingDecision{}, ports.ErrNotConfigured
}

func (NotConfigured) ListSpecAvailability(context.Context, string) ([]ports.GPUSpecAvailability, error) {
	return nil, ports.ErrNotConfigured
}
