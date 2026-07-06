# CORE-NET-OPENAPI-CONSOLE-A — Console network backend contract gaps

完成日期：2026-07-06
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
验证结果：local/logic verified

> 边界声明：本批次只补 ANI Core `/api/v1/networks/*` 契约、Gateway handler、ports/adapters 与 Core SDK/schema 生成物；不修改 Console 前端项目，不新增 Services 路径，不暴露 Kube-OVN/provider 内部 ID。

## 背景

Console 网络管理文档要求补齐五类 Core 网络资源的后端缺口：

- 安全组 rules 整包 PATCH。
- 路由单条 GET/DELETE。
- 子网按 VPC 服务端筛选。
- 删除存在依赖时返回 `409 CONFLICT`。
- 创建时补齐 VPC/subnet/gateway/route 冲突校验。

## 实现了什么

1. **OpenAPI 契约**
   - 新增 `PATCH /api/v1/networks/security-groups/{security_group_id}`，operationId `updateNetworkSecurityGroup`，RBAC `scope:networks:update`。
   - 新增 `UpdateNetworkSecurityGroupRequest`，要求 `idempotency_key` 与 `rules`，支持可选 `description`。
   - 新增 `GET /api/v1/networks/routes/{route_id}` 与 `DELETE /api/v1/networks/routes/{route_id}`。
   - `GET /api/v1/networks/subnets` 新增 query `vpc_id`。
   - VPC / 子网 / 安全组 / 负载均衡 DELETE 声明 `409`。
   - route create 声明 `409`。
2. **Gateway handler**
   - 注册 SG PATCH、route GET、route DELETE。
   - subnet list 将 `vpc_id` query 传入 service。
   - PATCH SG 只做 rules 整包替换；不新增 `/rules` 子路径。
3. **NetworkService / adapter**
   - 新增 `NetworkSecurityGroupUpdateRequest`、`UpdateSecurityGroup`、route `GetRoute/DeleteRoute`。
   - subnet list 支持按 `tenant_id + vpc_id` 过滤。
   - subnet create 校验 gateway 必须为合法 IPv4 且在 CIDR 内。
   - LB create 校验 VPC 存在；提供 subnet 时必须属于目标 VPC。
   - route create 校验 VPC 存在，同一 VPC 下 destination CIDR 冲突返回 `ErrConflict`。
   - delete 前检查依赖：VPC 关联 subnet/LB/route/instance，subnet 关联 LB/instance 时返回 `ErrConflict`。
4. **Metadata-backed runtime**
   - `MetadataNetworkStore.CountDeleteDependencies` 使用既有表查询依赖，不新增表。
   - instance 依赖检查复用 `workload_instances.vpc_id/subnet_id`。
5. **SDK / schema**
   - 重新生成 Core/Services SDK 生成物。
   - 修复 SDK Alpha 生成/校验脚本：OpenAPI templated server URL 生成 SDK 时规范化为本地 mock 默认 URL，校验脚本使用可写临时 Go cache 与当前 Python 解释器。

## 单测覆盖

- SG PATCH 更新 description + rules。
- subnet list 按 VPC 过滤。
- subnet gateway 不在 CIDR 内 -> `ErrInvalid`。
- LB create 无效 VPC -> `ErrNotFound`。
- LB create subnet 不属于目标 VPC -> `ErrInvalid`。
- VPC 仍有关联 subnet 时 delete -> `ErrConflict`。
- subnet 仍被 LB 使用时 delete -> `ErrConflict`。
- route duplicate destination -> `ErrConflict`。
- route GET/DELETE 成功与删除状态。

## 验证命令

```bash
python3 scripts/validate_yaml.py api/openapi/v1.yaml
python3 scripts/validate_network_alpha_contract.py
python3 scripts/validate_core_alpha_contract.py
python3 scripts/validate_sdk_beta.py
python3 scripts/validate_sdk_alpha.py
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime -run 'TestLocalNetworkService|TestMetadataNetworkStore'
GOCACHE=/tmp/ani-go-cache go test ./services/ani-gateway/internal/router -run 'TestNetworkAPI'
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/runtime
python3 scripts/validate_component_imports.py --root .
git diff --check
```

结果：上述命令均通过。

## 已知边界

- `make test` / `make validate-architecture` 在当前环境因 Makefile 调用 `python` 且系统无 `python` 命令失败；已用 `python3` 等价命令验证架构脚本。
- `python3 scripts/validate_core_api_compatibility.py` 报历史基线差异 `GET /branding changed operationId`，与本次网络改动无关。
- 本批次不做 VPC/Subnet/LB PATCH，不做 SG rules 独立 CRUD，不做子网 IP 分配列表，不做 SG 绑定/解绑实例。
