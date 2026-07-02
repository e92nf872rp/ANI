package router

import (
	runtimeadapter "github.com/kubercloud/ani/pkg/adapters/runtime"
	"github.com/kubercloud/ani/pkg/ports"
)

// InstanceWorkloadRuntime wires workload provider adapters for /instances.
type InstanceWorkloadRuntime struct {
	Provider     string
	DryRun       ports.WorkloadProviderDryRun
	Apply        ports.WorkloadProviderApply
	StatusReader ports.WorkloadProviderStatusReader
	Lifecycle    ports.WorkloadInstanceLifecycleExecutor
	Ops          ports.WorkloadInstanceOps
}

func DefaultInstanceWorkloadRuntime() InstanceWorkloadRuntime {
	return InstanceWorkloadRuntime{
		Provider:     "local",
		DryRun:       runtimeadapter.NewLocalProviderDryRun(),
		Apply:        runtimeadapter.NewLocalProviderApply(runtimeadapter.WithProviderApplyEnabled(true)),
		StatusReader: runtimeadapter.NewLocalProviderStatusReader(),
		Ops:          runtimeadapter.NewLocalInstanceOpsGuard(runtimeadapter.WithInstanceOpsEnabled(true)),
	}
}
