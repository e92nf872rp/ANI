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
	tenantAdmin := postgres.NewPostgresTenantAdminStore(deps.DB)
	tenantStore := postgres.NewPostgresTenantStore(deps.DB)

	tenantPlanSvc := service.NewTenantPlanService(plans, audit, coreQuota, coreTenantPlans)
	tenantSvc := service.NewTenantService(plans, coreTenants, coreTenantPlans, coreQuota, audit)
	tenantAdminSvc := service.NewTenantAdminService(coreTenantAdmins, coreTenants, tenantAdmin, audit)
	// SSO adapter 在 Issue-005 接入；lifecycle/quota_change 经 tenantStore 直读 PG（与 audit_logs 先例一致）。
	tenantListSvc := service.NewTenantListService(plans, coreTenants, coreTenantAdmins, coreQuota, tenantStore, audit, nil, nil)

	bootstrap.RunGRPC(cfg.GRPCPort, func(s *grpc.Server) {
		tenantPlanSvc.Register(s)
		tenantSvc.Register(s)
		tenantAdminSvc.Register(s)
		tenantListSvc.Register(s)
	}, deps)
}
