# IN-NETWORK-VPC-DELETE-PROTECTION-A：VPC 删除保护（有存活关联禁止删除）

> 状态：已完成（live 验证通过，10.10.1.66 ani-system 镜像 `dev-20260916-vpc4`）
> 批次类型：hotfix（修复 kjs-study 测试异常记录 VPC-4）
> 分支：fix/network-sg-vpc-and-instance-filter
> 前序批次：IN-NETWORK-SG-BINDING-DERIVED-A（`network-sg-binding-derived-view.md`）

## 1. 背景与问题现象

依据 `kjs-study/修复bug/VPC子网安全组问题分析与修复记录.md` VPC-4 的定责结论与方案 A（2026-09-16 决策：防御式禁止删除 + 后端提示），修复「存在级联资源时删除 VPC，级联资源依然存在」：

- `DELETE /networks/vpcs/{vpc_id}` 此前只把 VPC 置 `deleted` 后 upsert，不校验子网/安全组/LB/路由等下级资源——删除成功后留下孤儿下级资源（测试原话："成功删除，但级联资源依然存在"）。

## 2. 根因

1. `DeleteVPC`（network_service.go）既不校验存活关联，也不做级联清理/级联删除；`DeleteSubnet`/`DeleteSecurityGroup` 同构；
2. `GetOverview` 的 `DeleteRisks` 已声明「删除 VPC 影响其子网、路由与 LB」，但仅是概览文案，删除链路无任何落地——声明与行为脱节；
3. provider 侧仅 apply（创建/更新），无 delete/卸载逻辑。

## 3. 方案

用户已决策方案 A（防御式禁止删除）：有存活关联 → 拒绝删除并给出数量明细提示，引导先清理下级资源。不做级联删除（与产品语义一致，且避免 provider 无卸载能力下的半删状态）。

落地时修正了方案里的一处盲区：原方案遍历内存 map 判断关联——但网关重启后内存 map 为空，保护会静默失效放行（与安全组-3/4 的 store 模式校验缺陷同源）。因此 store 模式下全部以持久层为准。

## 4. 实现

1. **ports（`pkg/ports/network_resources.go`）**：`NetworkResourceStore` 接口新增 `ListLoadBalancers(tenantID)`/`ListRoutes(tenantID)`——`network_load_balancers`/`network_routes` 表本就持久化 LB/路由（此前只有 Upsert 无 List），补齐后关联计数才能查持久层；
2. **store（`pkg/adapters/runtime/network_store.go`）**：`MetadataNetworkStore` 实现两个新 List 方法（显式列清单、`state <> 'deleted'`、listeners JSONB 反序列化，与既有 List 同风格）；
3. **service（`pkg/adapters/runtime/network_service.go`）**：
   - `DeleteVPC` 重写为双分支：store 模式 `store.GetVPC` 查 VPC（修复重启后删除 404 的隐患）→ `vpcAssociationCounts` 查持久层四类存活关联 → 命中拒绝；内存模式维持原查 map + `vpcAssociationCountsMemory` 计数；
   - `vpcAssociationConflict`：任一类非 0 → `fmt.Errorf("%w: cannot delete VPC %s: %d subnet(s), %d security group(s), %d load balancer(s), %d route(s) still exist; delete them first", ports.ErrConflict, ...)`；`ErrConflict` 经既有 `writeNetworkError` 映射 409 `CONFLICT`，handler 零改动；
   - store 模式删除成功后同步更新内存 map（如存在），保持双写一致。
4. **契约**：无变更——`deleteNetworkVPC` 响应本就声明 `409 Conflict`（v1.yaml:6220）；错误消息走既有 `{code,message}` 结构。
5. **迁移**：无 DB schema 变更。

## 5. 测试

- `network_service_test.go` 新增 2 个：
  - `TestLocalNetworkServiceDeleteVPCBlockedByLiveAssociations`（内存模式）：VPC 下有存活子网+安全组 → 删除 `ErrConflict` 且消息含四类数量；被拒后 VPC 仍 `available`；清理关联后删除放行置 `deleted`；
  - `TestLocalNetworkServiceDeleteVPCValidatesAssociationsViaStore`（store 模式）：fake 按表路由行数据——子网/安全组属于目标 VPC、LB/路由属于其他 VPC（不计数），删除被拒且无任何 upsert 落库；各表清空后删除放行，最后一条 exec 为 `INSERT INTO network_vpcs` 且 state=`deleted`；
- 测试基建：`fakeMetadataTx` 新增 `queryRows map[string]ports.Rows`（按 SQL 表名关键字路由 Rows，支持一次事务查多表场景）；既有 `TestLocalNetworkServicePersistsCreateAndDelete` 补 GetVPC 行数据与空结果集适配（`network_store_test.go`）；
- 本地门禁：gofmt 触碰文件干净、`go build ./pkg/...` + `services/ani-gateway` 全量、runtime 包全量单测（仅余 2 个存量 Windows symlink 失败，与本批无关）、router 包单测通过、`git diff --check` 通过。

## 6. Live 验证（10.10.1.66 ani-system，镜像 `dev-20260916-vpc4`）

部署链路按 `kjs-study/更新K8s测试环境的auth-service和gateway操作步骤.md`：构建机（192.168.18.35，工作区 `/root/ani-build/filter-20260915`）上传 3 个改动源文件后台构建推送 `dev-20260916-vpc4` → `kubectl set image -n ani-system` 滚动 → Pod `ani-gateway-7c788bb75f-hps5q` 1/1 Running、`/healthz` 200。租户 token（tenant-a）实测：

- **核心证据**：删除带存活关联的 `test-vpc-ly123`（`vpc_68633c00`，1 个存活子网）→ **409 `CONFLICT`**：`cannot delete VPC vpc_68633c00-02ab-4ff2-ac57-11b24784e63f: 1 subnet(s), 0 security group(s), 0 load balancer(s), 0 route(s) still exist; delete them first`（修复前该操作直接成功、留下孤儿子网）；
- 被拒后 `GET /networks/vpcs/{id}` 仍 `available`，无副作用；
- 对照：新建无关联测试 VPC → 删除 **200 deleted**（保护不误伤正常删除）；删除不存在的 VPC → 404；
- 测试 VPC 已清理，环境干净。

## 7. 已知边界与后续项

- 关联计数以 store List 的 `state <> 'deleted'` 为存活口径——`pending/failed` 等非 deleted 状态同样阻止删除（防御式语义，宁可拦错不漏删）；实测中 `test-vpc-ly123` 的安全组关联为 deleted 态故计 0，符合预期；
- 实例对 VPC/子网的引用（`workload_instances.network_summary`）不在四类计数内——实例删除有自己的生命周期链路，VPC 保护仅覆盖网络域下级资源；
- 真实 provider delete/卸载能力（provider 仅 apply）与 `DeleteSubnet`/`DeleteSecurityGroup` 的同类关联保护（如子网下有 IP 分配/LB 引用）为后续批次候选。
