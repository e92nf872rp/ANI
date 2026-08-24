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
	coreTenantPlans := core.NewTenantPlanSvcClient()
	coreTenantAdmins := core.NewTenantAdminSvcClient()

	tenantPlanSvc := service.NewTenantPlanService(plans, audit, coreQuota, coreTenantPlans)
	tenantSvc := service.NewTenantService(plans, coreTenants, coreTenantPlans, coreQuota, audit)
	tenantAdminSvc := service.NewTenantAdminService(coreTenantAdmins)

	bootstrap.RunGRPC(cfg.GRPCPort, func(s *grpc.Server) {
		tenantPlanSvc.Register(s)
		tenantSvc.Register(s)
		tenantAdminSvc.Register(s)
	}, deps)
}
