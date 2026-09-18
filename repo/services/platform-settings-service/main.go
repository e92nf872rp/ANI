package main

import (
	"log"
	"os"

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

	// 双层凭证（与 inference-service 一致）：AUTH_SERVICE_GRPC_ADDR +
	// AUTH_SERVICE_MINT_SECRET 都配置时经 auth-service 动态 mint 短时 JWT；
	// 否则回退 CORE_API_TOKEN 静态兜底。
	if _, err := core.SetupMinter(
		os.Getenv("AUTH_SERVICE_GRPC_ADDR"),
		os.Getenv("AUTH_SERVICE_MINT_SECRET"),
	); err != nil {
		log.Fatalf("setup core minter: %v", err)
	}

	coreClient := core.NewCorePlatformUserClient()
	auditStore := postgres.NewPostgresPlatformAdminAuditStore(deps.DB)
	platformAdminSvc := service.NewPlatformAdminService(coreClient, auditStore)

	bootstrap.RunGRPC(cfg.GRPCPort, func(s *grpc.Server) {
		platformAdminSvc.Register(s)
	}, deps)
}
