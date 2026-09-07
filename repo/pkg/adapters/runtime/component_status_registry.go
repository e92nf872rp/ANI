package runtime

import (
	"github.com/kubercloud/ani/pkg/ports"
)

// componentRegistration 组件状态静态注册表条目。
// 基线为 2026-09-04 集群实测（kjs-study/组件状态相关文档/组件状态实测清单.md
// 与当日 `kubectl get deploy,sts,ds -A` 地面真值核对）；覆盖不到的组件
// （如 kms-sm4-live-fixture、model-import-worker）宁缺勿错，不纳入。
type componentRegistration struct {
	// Name 是 K8s 工作负载对象名（deployment/sts/ds 的 metadata.name）。
	Name string
	Kind ports.ComponentKind
	// Namespace 为对象所在 namespace。
	Namespace string
	// Group 为 service / dependency / platform 分组。
	Group string
	// ServiceName 仅 service 组非空：canonical 服务名，用于与
	// PlatformServiceHealthReader 的 scrape_status 按 service_name 融合。
	ServiceName string
}

// componentStatusRegistry 平台组件状态注册表（代码内静态声明，不做动态注册/DB 表）。
var componentStatusRegistry = []componentRegistration{
	// service 组：七个 ANI 自研服务（Prometheus 埋点覆盖集）。
	{Name: "ani-gateway", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupService, ServiceName: "ani-gateway"},
	{Name: "ani-auth-service", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupService, ServiceName: "auth-service"},
	{Name: "model-service", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupService, ServiceName: "model-service"},
	{Name: "task-service", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupService, ServiceName: "task-service"},
	{Name: "inference-service", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupService, ServiceName: "inference-service"},
	{Name: "tenant-service", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupService, ServiceName: "tenant-service"},
	{Name: "ani-metering-service", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupService, ServiceName: "metering-service"},

	// dependency 组：平台基础依赖（第三方，无埋点，纯 K8s 信号）。
	{Name: "ani-reconcile-ha-postgres", Kind: ports.ComponentKindStatefulSet, Namespace: "ani-system", Group: ports.ComponentGroupDependency},
	{Name: "ani-reconcile-ha-redis", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupDependency},
	{Name: "ani-reconcile-ha-nats", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupDependency},
	{Name: "ani-s05-minio", Kind: ports.ComponentKindDeployment, Namespace: "ani-s05-objectstore", Group: ports.ComponentGroupDependency},
	{Name: "sprint13-milvus", Kind: ports.ComponentKindDeployment, Namespace: "ani-s06-vectorstore", Group: ports.ComponentGroupDependency},

	// platform 组：其他平台组件（会话网关、reconcile、控制台前端、OIDC、
	// AI 业务服务、可观测底座、鉴权适配器）。
	{Name: "ani-session-gateway", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupPlatform},
	{Name: "ani-reconcile-worker-a", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupPlatform},
	{Name: "ani-reconcile-worker-b", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupPlatform},
	{Name: "ani-console", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupPlatform},
	{Name: "ani-dex", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupPlatform},
	{Name: "kb-service", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupPlatform},
	{Name: "rag-engine", Kind: ports.ComponentKindDeployment, Namespace: "ani-system", Group: ports.ComponentGroupPlatform},
	{Name: "sprint13-prometheus", Kind: ports.ComponentKindDeployment, Namespace: "ani-s07-observability", Group: ports.ComponentGroupPlatform},
	{Name: "ani-loki", Kind: ports.ComponentKindDeployment, Namespace: "ani-s07-observability", Group: ports.ComponentGroupPlatform},
	{Name: "envoy-authz-adapter", Kind: ports.ComponentKindDeployment, Namespace: "ani-aigw", Group: ports.ComponentGroupPlatform},
}

// componentStatusGroups 注册表分组的固定输出顺序。
var componentStatusGroups = []string{
	ports.ComponentGroupService,
	ports.ComponentGroupDependency,
	ports.ComponentGroupPlatform,
}
