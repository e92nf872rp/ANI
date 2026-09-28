package main

import (
	"log"
	"os"

	"github.com/kubercloud/ani/services/pkg/bootstrap"
	"github.com/kubercloud/ani/services/tenant-service/internal/config"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/adapters/core"
	"github.com/kubercloud/ani/services/tenant-service/internal/repo/adapters/postgres"
	"github.com/kubercloud/ani/services/tenant-service/internal/service"
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
		bootstrapEnv("AUTH_SERVICE_GRPC_ADDR"),
		bootstrapEnv("AUTH_SERVICE_MINT_SECRET"),
	); err != nil {
		log.Fatalf("setup core minter: %v", err)
	}

	plans := postgres.NewPostgresTenantPlanStore(deps.DB)
	audit := postgres.NewPostgresAuditStore(deps.DB)
	coreQuota := core.NewQuotaSvcClient()
	coreTenants := core.NewTenantSvcClient()
	coreTenantPlans := core.NewTenantPlanSvcClient()
	coreTenantAdmins := core.NewTenantAdminSvcClient()
	tenantAdmin := postgres.NewPostgresTenantAdminStore(deps.DB)
	tenantStore := postgres.NewPostgresTenantStore(deps.DB)

	tenantPlanSvc := service.NewTenantPlanService(plans, audit, coreQuota, coreTenantPlans)
	tenantSvc := service.NewTenantService(plans, coreTenants, coreTenantPlans, coreQuota, tenantStore, audit, coreTenantAdmins, nil, nil, tenantAdmin)
	tenantAdminSvc := service.NewTenantAdminService(coreTenantAdmins, coreTenants, tenantAdmin, audit)

	bootstrap.RunGRPC(cfg.GRPCPort, func(s *grpc.Server) {
		tenantPlanSvc.Register(s)
		tenantSvc.Register(s)
		tenantAdminSvc.Register(s)
	}, deps)
}

func bootstrapEnv(key string) string {
	return os.Getenv(key)
}
