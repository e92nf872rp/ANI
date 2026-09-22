# IN-NETWORK-SG-VPC-AND-INSTANCE-FILTER-A：安全组 vpc_id 建模与实例列表 VPC/子网过滤

> 状态：已完成（live 验证通过，10.10.1.66 ani-system 镜像 `dev-20260916-network2`）
> 批次类型：hotfix（修复 kjs-study 测试异常记录 安全组-3/4、VPC-3、子网-3）
> 分支：fix/network-sg-vpc-and-instance-filter

## 1. 背景与问题现象

依据 `kjs-study/修复bug/VPC子网安全组问题分析与修复记录.md` 的结论与方案（方案 A：加可空列最小侵入），修复四个测试异常：

- **安全组-3**：`GET /networks/security-groups?vpc_id=…` 按 VPC 过滤无效；
- **安全组-4**：创建安全组指定 `vpc_id` 未绑定——响应/详情无 vpc_id 回显，且部署实测发现带合法历史 VPC 的创建请求直接 404 `vpc not found`；
- **VPC-3**：实例列表 `GET /instances?vpc_id=…` 后端忽略参数，返回全部租户实例（VPC 详情「关联资源」场景）；
- **子网-3**：实例列表 `GET /instances?subnet_id=…` 同样不生效。

## 2. 根因

- **安全组-4（契约未跟随 + store 模式校验误判）**：契约自 PR #99（`8b71d83`）起已在 `CreateNetworkSecurityGroupRequest`、`NetworkSecurityGroup` schema 与列表参数中声明 `vpc_id`（历史资源 nullable），但实现五层均未跟随：port record 无 `VPCID` 字段、`network_security_groups` 表无列、handler 不解析不回显、service 校验只查内存 map。部署实测暴露第二层根因：`CreateSecurityGroup` 的 VPC 校验只查内存 map `s.vpcs`，**gateway 进程重启后内存 map 为空**，store 模式下（`MetadataNetworkStore` 已在 `INSTANCE-NETWORK-STORE-READ-RLS-A` 注入）创建安全组绑定任何历史 VPC 都被误判 `vpc not found`——本地内存 profile 测试发现不了该缺陷。
- **安全组-3（列表 memory-only）**：`ListSecurityGroups` 与已 store-first 的 `GetSecurityGroup` 不一致，纯内存实现；gateway 重启后列表返回 0 条（DB 中 12 条历史安全组不可见），vpc_id 过滤无从谈起。
- **VPC-3/子网-3（实例列表缺参数）**：`instanceListRequestFromQuery` 不解析 vpc_id/subnet_id，`WorkloadInstanceListRequest` 无对应字段，`matchesInstanceList` 无归属匹配；孤儿合并路径同样不遵守（重启后所有真实实例按孤儿合并时会无条件返回）。

## 3. 实现

契约优先，逐层跟随：

1. **契约 `repo/api/openapi/v1.yaml`**：安全组 vpc_id 三处（创建请求/响应 schema/列表过滤参数）自 PR #99 已存在，本批不重复改动；仅 `/instances` GET 新增 `vpc_id`、`subnet_id` 两个 query 参数（description 注明按所属 VPC/子网过滤实例）。
2. **ports（`pkg/ports/network_resources.go`、`pkg/ports/workload_runtime.go`）**：`NetworkSecurityGroupRecord` 增 `VPCID`/`BoundInstanceCount`；`NetworkSecurityGroupCreateRequest` 增 `VPCID`；`WorkloadInstanceListRequest` 增 `VPCID`/`SubnetID`；`NetworkResourceStore` 接口补 `ListSecurityGroups`（`GetSecurityGroup` 既有）。
3. **store（`pkg/adapters/runtime/network_store.go`）**：新增 `MetadataNetworkStore.ListSecurityGroups`——`SELECT tenant_id::text, security_group_id, COALESCE(vpc_id,''), name, COALESCE(description,''), rules, state, COALESCE(reason,''), created_at, updated_at FROM network_security_groups WHERE tenant_id=$1::uuid AND state <> 'deleted' ORDER BY updated_at DESC`，`rules` JSONB 非空时反序列化到 `record.Rules`。
4. **service（`pkg/adapters/runtime/network_service.go`）**：
   - `CreateSecurityGroup` VPC 校验改走新增 helper `resolveVPCForValidation`：store 模式优先查持久层（重启后内存 map 不含历史 VPC），否则回退内存 map（显式比对租户归属）；VPC 不存在或 `deleted` → `ports.ErrNotFound`。语义对齐 `CreateSubnet` 的 VPC 校验（vpc_id 契约上可空，一旦提供必须校验归属与存活）；
   - `ListSecurityGroups` store-first：store 分支调 `store.ListSecurityGroups` 后依次应用 VPCID 精确过滤、Name 前缀、Keyword（SecurityGroupID 前缀）、State 过滤，RLock 填 `BoundInstanceCount`（复用 `securityGroupBoundInstanceCountLocked`）后按 UpdatedAt 倒序；内存分支行为不变。
5. **实例过滤（`pkg/adapters/runtime/instance_service.go`）**：新增导出 `MatchesInstanceNetwork`（显式传 vpc_id/subnet_id 时按 `record.Network.VPCID/SubnetID` 精确匹配，未传不过滤），接入 `matchesInstanceList`。
6. **gateway router（`services/ani-gateway/internal/router/`）**：
   - `instances.go`：`instanceListRequestFromQuery` 解析 `vpc_id`/`subnet_id`；孤儿合并补 `MatchesInstanceNetwork` 遵守（与 keyword/state 同列）；
   - `network_resources.go`：`createSecurityGroup` 透传 `VPCID`；`listSecurityGroups` 解析 `vpc_id`；`networkSecurityGroupResponse` 增 `vpc_id`（omitempty）与 `bound_instance_count`（omitempty），`networkSecurityGroupFromRecord` 填充。
7. **迁移（`deploy/migrations/20260916120000_network_security_groups_vpc_id.sql`）**：`ALTER TABLE network_security_groups ADD COLUMN IF NOT EXISTS vpc_id TEXT`——可空、无 NOT NULL/外键（方案 A 最小侵入），存量行 NULL 对齐契约"历史资源可为空"；既有读写 SQL 均为显式列清单，不受影响。atlas.sum 经 atlas v1.3.4（`curl -sSL https://atlasgo.sh | sh` 在构建机 LF 环境安装）重算，并对旧文件集重算哈希与仓库现存 atlas.sum 首行完全一致完成算法一致性验证（本地 Windows CRLF checkout 无法直接算）。

## 4. 测试

- `network_service_test.go` 新增 4 个：`TestLocalNetworkServiceCreateSecurityGroupValidatesVPCViaStore`（store 模式校验命中 `network_vpcs` 查询、INSERT 带 vpc_id）、`TestLocalNetworkServiceCreateSecurityGroupRejectsMissingVPCViaStore`（VPC 查询失败 → `ErrNotFound` 且无写操作）、`TestMetadataNetworkStoreListsSecurityGroupsWithVPC`（SQL 行扫描 + rules 反序列化 + 空 vpc_id 兼容）、`TestLocalNetworkServiceListsSecurityGroupsFromStoreByVPC`（store-first 列表 + VPCID 过滤）；
- `instance_service_test.go` 新增 `TestMatchesInstanceNetworkConsistentForOrphans`；
- `network_resources_test.go` 新增 `TestNetworkAPISecurityGroupVPCTaggingAndFilter`（HTTP 层：创建带 vpc_id → 响应回显 → 列表 vpc_id 过滤命中）；
- `plan_audit_store_test.go`/`operation_store_test.go` 的 `fakeMetadataRow.Scan` 各补 `*ports.NetworkResourceState` case（新增列扫描联动的测试基建）。
- 本地门禁：`gofmt`（改动文件干净）、`go build ./pkg/...` + `services/ani-gateway` 全量、runtime/router 定向单测通过（`TestSandboxFileScripts*` 2 项为存量 Windows symlink 环境必挂项，与本批无关）；`git diff --check` 通过。

## 5. Live 验证（10.10.1.66 ani-system，镜像 `dev-20260916-network2`）

部署链路按 `kjs-study/更新K8s测试环境的auth-service和gateway操作步骤.md`：构建机构建推送 → 迁移 `20260916120000` 应用（列已加）→ `kubectl set image` 滚动 → Pod 1/1 Running、`/healthz` 200。租户 token（tenant-a）实测四项全过：

- **安全组-4**：创建绑定历史 VPC 成功（修复前 404），`vpc_id` 正确回显、`state=available`；绑定不存在的 VPC → 404；
- **安全组-3**：重启后列表可见 12 条历史安全组（修复前 0 条）；`vpc_id` 过滤精确命中新建安全组且 `all_match_vpc=true`；
- **VPC-3**：全量 59 条实例 → `vpc_68633c00` 过滤 2 条且 `all_match_vpc=true`（`network.vpc_id` 嵌套结构）；不存在的 VPC → 0 条；
- **子网-3**：`subnet_4de8a1c2` 过滤 2 条全匹配；不存在的子网 → 0 条。

验证产生的测试数据已清理（verify-sg 走 API 删除；diag-sg-novpc 因 `DeleteSecurityGroup` memory-only 缺陷改经 DB UPDATE `state='deleted'` 清理）。

## 6. 已知边界与后续项

- **同源校验缺陷（本次未改，建议后续批次）**：`CreateSubnet`（network_service.go）、`CreateLoadBalancer`、`CreateRoute` 与 `CreateSecurityGroup` 修复前同源——VPC/引用校验只查内存 map，store 模式下重启后同样会把已存在资源误判 not found。本次只修了用户指名的安全组路径，其余建议按同一 helper 模式收口。
- **安全组删除/规则 API 仍 memory-only**：`DeleteSecurityGroup`、`/security-groups/{id}/rules` CRUD 未接 store，重启后对 DB 历史安全组操作 404（验证清理时已实际暴露）。删除链路建议后续 store 化。
- **安全组绑定实例计数**：`bound_instance_count` 沿用内存链路计算口径，store 分支下重启后该字段为 0，不影响过滤正确性。
- **atlas v1.3.4 validate 存量警告**：`2026082x_001_*` 等历史文件存在同版本号重复（命名风格解析问题），hash 本身成功；与本次迁移无关。
