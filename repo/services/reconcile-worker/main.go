package main

import (
	"github.com/kubercloud/ani/pkg/bootstrap"
	"github.com/kubercloud/ani/services/reconcile-worker/internal/config"
)

func main() {
	cfg := config.Load()
	deps := bootstrap.MustConnect(cfg)
	defer deps.Close()

	bootstrap.RunWorkloadReconcileWorker(deps)
}
