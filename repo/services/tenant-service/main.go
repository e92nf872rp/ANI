package main

import (
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

	plans := postgres.NewPostgresTenantPlanStore(deps.DB)
	audit := postgres.NewPostgresTenantPlanAuditStore(deps.DB)
	coreQuota := core.NewQuotaSvcClient()
	coreTenants := core.NewTenantSvcClient()

	// 两个 gRPC service 注册到同一个 server。
	tenantPlanSvc := service.NewTenantPlanService(plans, audit, coreQuota, coreTenants)
	tenantSvc := service.NewTenantService(plans, coreTenants, coreQuota, audit)
	tenantAdminSvc := service.NewTenantAdminService()

	bootstrap.RunGRPC(cfg.GRPCPort, func(s *grpc.Server) {
		tenantPlanSvc.Register(s)
		tenantSvc.Register(s)
		tenantAdminSvc.Register(s)
	}, deps)
}
