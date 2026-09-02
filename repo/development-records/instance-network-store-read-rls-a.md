# INSTANCE-NETWORK-STORE-READ-RLS-A

> 日期：2026-08-27 ~ 2026-09-01
> 范围：ANI Core / Gateway network runtime / PostgreSQL RLS / GPU 容器实例创建链路
> 状态：live passed（K8s 测试环境 10.10.1.66，实例创建 201 → provisioning → Volcano 调度排队）

## 问题背景

前端对接 `POST /api/v1/instances` 创建 GPU 容器实例时，引用已创建的 VPC 报错：

```text
resolve instance vpc "vpc_xxx": capability resource not found
```

## 根因（三层叠加，逐层暴露）

1. **store 只写不读**：`NetworkResourceStore` 端口只有 Upsert 写方法，没有任何读方法；`MetadataNetworkStore` 实现全部是 INSERT/UPDATE，无 SELECT。`LocalNetworkService` 的 Get/List 只查进程内存 map。
2. **Gateway 双实例内存不共享**：`/networks/*` 路由的 network service 实例（`network_runtime.go` `NETWORK_PROVIDER` 未配置时纯内存）与实例创建 resolver 的实例（`bootstrap.ConnectInstanceService` 内部另建）是两个不同对象；即使不重启进程，resolver 也查不到前端创建的 VPC。
3. **RLS 必拒策略**：DB 用户从 superuser `ani` 切换为普通角色 `ani_app_user` 后 RLS 真正生效。初始迁移（20260501_001）建的网络/存储/workload 表只有单条 RESTRICTIVE `tenant_isolation` policy、无任何 PERMISSIVE policy——PostgreSQL 规则是没有 PERMISSIVE policy 通过时拒绝一切行读写，无论 `app.current_tenant_id` 是否正确。此前被 superuser 连接完全掩盖（`20260825_001_workload_instances_rls_fix.sql` 的迁移注释已记录同类问题）。

## 实现

- `pkg/ports/network_resources.go`：`NetworkResourceStore` 端口新增 5 个读方法：`GetVPC` / `ListVPCs` / `GetSubnet` / `ListSubnets` / `GetSecurityGroup`。
- `pkg/adapters/runtime/network_store.go`：`MetadataNetworkStore` 实现上述读方法，SELECT 经 `WithTenantTx` 注入租户上下文，未命中返回 `ErrNotFound`，与 `MetadataStorageStore.GetVolume/ListVolumes` 既有模式一致。
- `pkg/adapters/runtime/network_service.go`：`LocalNetworkService` 的 Get/List 路径在配置 store 时穿透读 DB，未配置时保留内存 fallback（对齐 `LocalStorageService` 模式）。
- `services/ani-gateway/network_runtime.go`：`newGatewayNetworkService` 在配置 `DATABASE_URL` 时给 `LocalNetworkService` 注入 metadata store（`kubeovn_rest` 与 local 两分支），并返回 close 函数；`main.go` 接线。
- `deploy/migrations/20260828_001_instance_resource_rls_fix.sql`：实例创建链路 8 张表（`network_vpcs`、`network_subnets`、`network_security_groups`、`storage_filesystems`、`storage_filesystem_attachments`、`platform_workloads`、`platform_workload_intents`、`instance_plan_audits`）按 `20260825_001` 三段式替换为 PERMISSIVE `platform_bypass` + `self` 双策略。
- `services/ani-gateway/Dockerfile`：GOPROXY / GOWORK=off / 本地 ani-tools COPY，适配离线构建环境。

## 真实验证

Gateway 镜像：`docker.changqingyun.cn/ani/ani-gateway:dev-20260827`。

环境变量（10.10.1.66 `ani-system`，`kubectl set env`）：`WORKLOAD_PROVIDER=kubernetes_rest`、`WORKLOAD_PROVIDER_APPLY_ENABLED=true`。

真实顺序已通过：

- 租户 `tenant-a` 密码登录 200；
- `POST /networks/vpcs` / `POST /networks/subnets` 201 并真实落库（RLS 放行）；
- `GET /networks/vpcs` 穿透读 DB 命中已落库记录；
- `POST /instances`（GPU 容器、引用新 VPC/Subnet）201，state=provisioning，provider=kubernetes，resource_refs 已写入；
- reconciler 不再报 `resource refs are required for Kubernetes observation`；最终停留 `QueuedInScheduler`（Volcano 调度排队，集群无空闲 GPU 节点，属容量问题，非代码问题）。

修复过程同时在测试环境 PG 上执行了 `refresh_tokens` / `api_keys` 的同型 RLS 修复用于登录验证，未纳入本迁移（登录链路由 auth 负责）。

## 能力边界

- 本批修复实例创建链路的网络资源解析、RLS 读写与 workload apply 接线；GPU runtime 到 `running` 仍受集群 GPU 容量与 `registry.local` 镜像可达性约束。
- 其余 20+ 张 RESTRICTIVE-only 表（`refresh_tokens` 等）不在本批范围，由各自链路按 `20260825_001` 三段式处理。
- 测试环境 PG Pod 曾重建导致历史数据丢失，本批验证基于重建后新数据；不外推为 production ready。

## 验证命令

```bash
# 代码
cd repo/pkg && go build ./...
cd repo/services/ani-gateway && go build ./...
go test -run "Network|VPC|Subnet" ./pkg/adapters/runtime/...
# RLS（服务器）
kubectl exec -it -n ani-system ani-reconcile-ha-postgres-0 -- psql -U ani -d ani \
  -c "SELECT polname, polpermissive FROM pg_policy WHERE polrelid = 'network_vpcs'::regclass;"
# 业务（NodePort 30080）
curl -s -X POST http://10.10.1.66:30080/api/v1/instances ... # 引用已创建 VPC，应 201 而非 NOT_FOUND
```
