# M1-NETWORK-A — Network Core API Dev Profile

完成日期：2026-05-20
对应 Sprint：Sprint 3（2026-05-20 提前启动；计划窗口 2026-06-16 ~ 06-30）
验证结果：make validate-network-alpha EXIT:0，make validate-core-alpha EXIT:0，make validate-architecture EXIT:0，python scripts/validate_yaml.py api/openapi/v1.yaml api/openapi/services/v1.yaml EXIT:0，make test EXIT:0，git diff --check EXIT:0

## 实现了什么

完成 Sprint 3 的第一个网络资源可验证切片：VPC、Subnet、SecurityGroup、LoadBalancer 已进入 Core API Alpha 契约，并在 Gateway 主路径提供 dev/local profile。网络资源响应统一包含租户、状态、原因和创建/更新时间，Services 可基于该契约开始网络资源联调。

本轮继续补齐持久化边界：新增 `NetworkResourceStore` port（能力抽象）、metadata adapter、bootstrap capability 和数据库迁移；网络资源 service 注入 store 后，create/delete 会写入租户隔离的网络资源表。当前仍是 dev/local profile，不宣称已具备 KubeOVN 真实 provider 执行能力。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | 新增 `/networks/vpcs`、`/networks/subnets`、`/networks/security-groups`、`/networks/load-balancers` path/schema/RBAC scope |
| `pkg/ports/network_resources.go` | 新增 | 新增网络资源 port（能力抽象）、状态模型和请求/响应记录 |
| `pkg/adapters/runtime/network_service.go` | 新增 | 本地 dev profile 实现 VPC/Subnet/SecurityGroup/LoadBalancer create/list/get/delete，并可接入持久化 store |
| `pkg/adapters/runtime/network_store.go` | 新增 | metadata-backed 网络资源持久化 adapter |
| `pkg/bootstrap/deps.go` | 修改 | 将网络资源 service/store 暴露到 bootstrap capabilities |
| `deploy/migrations/20260520000500_network_resources.sql` | 新增 | 新增网络资源表、索引和 RLS 租户隔离策略 |
| `services/ani-gateway/internal/router/network_resources.go` | 新增 | 注册网络资源 Gateway 主路径 |
| `scripts/validate_network_alpha_contract.py` | 新增 | 校验网络 path/schema/RBAC scope、Gateway route、Services API 边界和持久化迁移 |
| `Makefile` | 修改 | 新增 `make validate-network-alpha` |

## 完工标准达成

- [x] VPC、Subnet、SecurityGroup、LoadBalancer 的 Alpha path/schema/RBAC scope 进入 Core API 契约
- [x] Gateway 主路径具备 dev/local profile
- [x] 网络资源响应包含 tenant、state、reason、created_at、updated_at
- [x] 合同守卫防止网络 API 漂移，并确认网络 API 未进入 Services API 契约
- [x] 定向单元测试覆盖租户隔离、VPC/Subnet 依赖、安全组规则和 LoadBalancer dev profile
- [x] `NetworkResourceStore` port（能力抽象）、metadata adapter、bootstrap capability、RLS 迁移和持久化单元测试完成

## 备注

本切片先完成 contract-compatible dev profile 与持久化边界。后续 M1-NETWORK-A 可继续补 KubeOVN provider adapter，以及更细的路由表/规则更新语义。
