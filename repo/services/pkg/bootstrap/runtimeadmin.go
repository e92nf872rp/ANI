package bootstrap

import (
	"fmt"
	"log/slog"

	"github.com/kubercloud/ani/runtimeadmin"
)

const runtimeServiceNamespace = "ani"

func newRuntimeAdminForDeps(deps *Deps) (*runtimeadmin.Runtime, error) {
	if deps == nil {
		return nil, fmt.Errorf("bootstrap dependencies are required")
	}
	return newRuntimeAdmin(deps.ServiceName, dependencyProbeChecks(deps), deps.Logger)
}

func newRuntimeAdmin(serviceName string, checks []probeCheck, logger *slog.Logger) (*runtimeadmin.Runtime, error) {
	runtimeChecks := make([]runtimeadmin.Check, 0, len(checks))
	for _, check := range checks {
		runtimeChecks = append(runtimeChecks, runtimeadmin.Check{
			Name:        check.name,
			Criticality: runtimeadmin.Strong,
			Probe:       check.run,
		})
	}
	return runtimeadmin.New(runtimeadmin.Config{
		Identity: runtimeadmin.Identity{
			Namespace: runtimeServiceNamespace,
			Name:      serviceName,
		},
		Checks: runtimeChecks,
		Logger: logger,
	})
}
