# IN-NETWORK-SG-BINDING-DERIVED-A：安全组绑定派生视图（按实例记录反查）

> 状态：已完成（live 验证通过，10.10.1.66 ani-system 镜像 `dev-20260916-sg5`）
> 批次类型：hotfix（修复 kjs-study 测试异常记录 安全组-5）
> 分支：fix/network-sg-vpc-and-instance-filter
> 前序批次：IN-NETWORK-SG-VPC-AND-INSTANCE-FILTER-A（`network-sg-vpc-bind-and-instance-filter.md`）

## 1. 背景与问题现象

依据 `kjs-study/修复bug/VPC子网安全组问题分析与修复记录.md` 安全组-5 的定责结论，修复「安全组详情『关联资源』已绑定该安全组的资源未展示」：

- 前端安全组详情页已正确调用 `GET /networks/security-groups/{id}/bindings?target_type=instance`，后端接口存在但返回空——绑定数据断链，非前端渲染问题。

## 2. 根因

实例侧绑定与安全组侧查询使用**两套割裂的数据**：

1. **实例「更换安全组」（`WorkloadLifecycleChangeSecurityGroups`）只更新实例自身**：`applyApprovedLifecycleSummary` 重写 `record.Network.SecurityGroups`（instance_service.go），经 `persistLifecycleWithQuota` → `store.UpsertStatus` 持久化到 `workload_instances.network_summary`（JSONB）——**从不写独立的绑定表**；
2. **安全组详情的绑定查询依赖 `securityGroupBinds` 内存 map**：该 map 只有显式调用 bindings API（`CreateSecurityGroupBinding`）才写入，且**无持久化**——重启即丢。

于是最常见的绑定路径（实例侧更换安全组）在 `securityGroupBinds` 里永远没有记录 → bindings 查询永远为空。

部署实测还暴露第三层缺陷：`ListSecurityGroupBindings`/`CreateSecurityGroupBinding`/`DeleteSecurityGroupBinding` 的安全组存在性检查走 `securityGroupExistsLocked`（memory-only），网关重启后对 DB 历史安全组直接 404——绑定列表连派生数据都查不到。

## 3. 方案选择

分析文档给出两个方向，本批选定**方向②「查询统一」**并落地为派生视图：

- **不选方向①（实例侧同步写 binding 记录）**：需要 LocalInstanceService → LocalNetworkService 横向耦合，且 `securityGroupBinds` 是内存 map，还须新建绑定持久化表（加实体、加迁移、处理回填与双写漂移），违背"如无必要勿增实体"；
- **选定方向②**：实例记录（`workload_instances.network_summary`）本就是绑定关系的**持久化真实来源**（上批 live 验证已证明 network 摘要重启后可用），绑定查询按实例记录反查即可，无需新表、无需回填、天然覆盖存量数据；
- 落点在 service 层（注入 `WorkloadInstanceStore`）而非 gateway 组装：`bound_instance_count` 聚合字段在 service 内计算，同源修复避免"bindings 列表有值、详情计数为 0"的口径分裂。

## 4. 实现

1. **service（`pkg/adapters/runtime/network_service.go`）**：
   - `LocalNetworkService` 增 `instances ports.WorkloadInstanceStore` 字段 + `WithNetworkInstanceStore` 选项；
   - 新增 `derivedSecurityGroupBindings(ctx, tenantID)`：实例记录派生绑定视图（`map[sgID][]binding`）——遍历实例记录 `Network.SecurityGroups`，跳过 `deleting/deleted` 终态实例，生成确定性 binding_id（`sgb-inst-<instanceID>-<sgID>`）；
   - `ListSecurityGroupBindings`：存在性检查改走新增 `resolveSecurityGroupExists`（store 模式优先查持久层，与 `resolveVPCForValidation` 同模式）；显式 bindings 与派生视图合并，**同一实例派生优先去重**；`target_type`/`target_id` 过滤作用于两路数据；按 CreatedAt 倒序；
   - `securityGroupBoundInstanceCountLocked` 改签名接派生切片：计数 = 派生目标数 + 显式绑定中未被派生覆盖的目标数；`GetSecurityGroup`/`ListSecurityGroups`（store/内存两分支）先算一次派生视图（单次实例列表查询服务整个响应），未注入实例 store 时退化为原显式计数口径；
   - `CreateSecurityGroupBinding`/`DeleteSecurityGroupBinding` 存在性检查补 `storeBackedSecurityGroupExists`（调用方已持锁场景下仅查持久层），重启后对 DB 历史安全组不再 404。
2. **gateway wiring（`services/ani-gateway/network_runtime.go`）**：`newGatewayNetworkService` 两个分支（local+kubeovn_rest）在连接 metadata store 后同时注入 `WithNetworkInstanceStore(NewMetadataInstanceStore(metadata))`。
3. **契约**：无需改动——bindings 响应仍是既有 `NetworkSecurityGroupBinding` schema（派生记录字段与显式记录同构），`bound_instance_count` 语义不变（当前真实绑定实例数）。
4. **迁移**：无 DB schema 变更。

## 5. 测试

- `network_service_test.go` 新增 2 个：
  - `TestLocalNetworkServiceSecurityGroupBindingsDerivedFromInstanceRecords`：实例记录派生绑定（未绑定/终态实例不出现）；显式绑定与派生合并去重（派生 1 + 显式 2 = 去重后 2）；`target_id` 过滤作用于派生视图；`GetSecurityGroup`/`ListSecurityGroups` 的 `bound_instance_count` 同口径为 2；
  - `TestLocalNetworkServiceSecurityGroupBindingsFromStoreAfterRestart`：store 模式内存 map 为空时（重启模拟），绑定查询不 404 且派生绑定来自实例记录；
- 本地门禁：`gofmt -w` 触碰文件、`go build ./pkg/...` 与 `services/ani-gateway` 全量、runtime 包全量单测（仅余 2 个存量 Windows symlink 环境失败 `TestSandboxFileScripts*`，与本批无关）、router 包单测通过。

## 6. Live 验证（10.10.1.66 ani-system，镜像 `dev-20260916-sg5`）

部署链路按 `kjs-study/更新K8s测试环境的auth-service和gateway操作步骤.md`：构建机（192.168.18.35，工作区 `/root/ani-build/filter-20260915`）上传 3 个改动源文件后台构建推送 `dev-20260916-sg5` → `kubectl set image -n ani-system` 滚动 → Pod `ani-gateway-75c778bd4c-w8h8n` 1/1 Running、`/healthz` 200。租户 token（tenant-a）实测：

- **核心证据（修复前返回空）**：`GET /networks/security-groups/sg_39c87355-97b7-4d85-9d6b-866ff0bd1aaa/bindings?target_type=instance` 返回 **2 条派生绑定**——binding_id 为确定性 `sgb-inst-inst_57cb10df-...-sg_39c87355-...`、`sgb-inst-inst_295f7edc-...`，target_id 与实例记录 `network.security_groups` 完全一致；
- 该 SG 详情 `bound_instance_count=2` 正确（该 SG 为 deleted 态，故不在 12 条活跃列表中）；
- 12 个活跃安全组 bindings 均为空是正确结果（无实例引用）；不存在的 SG 查 bindings → 404 正确；
- **端到端可逆验证暴露存量能力缺口（非本批缺陷）**：创建新 SG 后调 `change_security_groups` lifecycle 换绑被 Kubernetes adapter 拒绝——`"capability operation is unsupported by this adapter: unsupported Kubernetes lifecycle action \"change_security_groups\""`。即真实 provider 环境下 Console「实例更换安全组」本身不可用，实例安全组引用的唯一写路径是创建时网络配置——派生视图恰好覆盖该真实路径。adapter 侧 lifecycle 能力补齐记为后续项。操作未生效（`instance_sgs` 保持原值）无需还原，新建 SG 删除成功（200），验证数据已清理、环境干净。

## 7. 已知边界与后续项

- 派生视图仅覆盖 `target_type=instance`；`network_interface`/`load_balancer` 目标仍走显式 bindings API（内存 map、无持久化），LB/NIC 绑定持久化待后续批次评估；
- 显式 instance binding 若指向实例记录中不存在的绑定（悬空记录），仍会出现在列表中（并集语义，不做清理）；
- `ListSecurityGroupRules` 及规则 CRUD 的存在性检查仍 memory-only（与上批记录一致，属安全组删除/规则 API store 化的后续批次范围）。
