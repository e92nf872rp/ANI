package main

import (
	"github.com/kubercloud/ani/services/pkg/bootstrap"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/config"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/adapters/core"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/repo/adapters/postgres"
	"github.com/kubercloud/ani/services/platform-settings-service/internal/service"
	"google.golang.org/grpc"
)

func main() {
	cfg := config.Load()
	deps := bootstrap.MustConnect(cfg)
	defer deps.Close()

	coreClient := core.NewCorePlatformUserClient()
	auditStore := postgres.NewPostgresPlatformAdminAuditStore(deps.DB)
	platformAdminSvc := service.NewPlatformAdminService(coreClient, auditStore)

	bootstrap.RunGRPC(cfg.GRPCPort, func(s *grpc.Server) {
		platformAdminSvc.Register(s)
	}, deps)
}
