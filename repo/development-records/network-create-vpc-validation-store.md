# IN-NETWORK-CREATE-VPC-VALIDATION-A — 路由/负载均衡/子网创建 VPC 校验 store 化收口

完成日期：2026-09-18
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
分支：`fix/routing-loadbalancer-bugs`（基于 main `114e15c`），提交 `ead195f`
验证结果：本批次相关 `go test` 全通过，`go build` 通过，`make validate-architecture` 通过，`gofmt -l` 改动文件无输出，`git diff --check` 干净；K8s 测试环境 ani-system（10.10.1.66:30080）实测 4 项断言全 PASS

## 实现了什么

收口 `IN-NETWORK-SG-VPC-AND-INSTANCE-FILTER-A` 明确登记的存量缺陷：**store 模式下创建网络资源对"网关重启前创建"的 VPC 一律 404 `capability resource not found: vpc not found`**。

同源三处创建接口 `CreateRoute` / `CreateLoadBalancer` / `CreateSubnet` 的 VPC 存在性校验只查内存 map `s.vpcs`；网关以 store 模式（`NETWORK_PROVIDER=kubeovn_rest` + `DATABASE_URL`）运行时，进程重启后内存 map 为空，DB 中的历史 VPC 被误判为不存在。此前 `CreateSecurityGroup` 已用 `resolveVPCForValidation`（store 优先查持久层）修复同一问题，本次把遗漏的三处对齐。

来源：测试异常分析文档 `kjs-study/修复bug/路由与负载均衡问题分析与修复记录.md`（未入库）。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/network_service.go` | 修改 | `CreateSubnet`（477-483）、`CreateLoadBalancer`（1402-1408）、`CreateRoute`（1509-1515）三处 VPC 校验由 `s.vpcs[...]` 直查改复用 `s.resolveVPCForValidation` |
| `pkg/adapters/runtime/network_service_test.go` | 修改 | 新增 3 个 store 模式正向用例 + 1 个反向表驱动用例（不存在 VPC / deleted VPC × 三类资源） |

无 OpenAPI 契约变更、无 handler 变更、无 DB 迁移、无生成物变更。

## 缺陷与修法

| # | 现象 | 根因 | 修法 |
|---|---|---|---|
| 路由-LB-1 | 前端"路由"/"负载均衡"页表单提交 404 `vpc not found`（列表 GET 正常） | `CreateRoute`/`CreateLoadBalancer` 校验目标 VPC 只查内存 `s.vpcs`，store 模式重启后为空 | 三处改复用 `resolveVPCForValidation`（store 分支 `store.GetVPC` 查持久层，非 store 回退内存 map） |

三处改动语义完全一致，示例（`CreateRoute`）：

```go
// store 模式必须查持久层：网关重启后内存 map 不含历史 VPC，
// 只查内存会把已存在的 VPC 误判为 not found（同 CreateSecurityGroup）。
vpc, ok := s.resolveVPCForValidation(ctx, request.TenantID, strings.TrimSpace(request.VPCID))
if !ok || vpc.State == ports.NetworkResourceDeleted {
    s.mu.Unlock()
    return ports.NetworkRouteRecord{}, fmt.Errorf("%w: vpc not found", ports.ErrNotFound)
}
```

设计说明：

- 原校验中的 `vpc.TenantID != request.TenantID` 判断由 `resolveVPCForValidation` 内部承担——store 分支 SQL 按 `tenant_id` 过滤，内存分支显式比对租户，两条路径均保留跨租户隔离语义（与 `CreateSecurityGroup` 完全一致）。
- 三处 `vpc_id` 必填语义原样保留（必填校验仍在方法前段）；`CreateSecurityGroup` 允许历史空 VPC 的分支语义不同，未改动。
- 新增注释仅落在三处改动点，未触碰方法内其它代码。

## 完工标准达成

- [x] 本批次相关 `go test`（`TestLocalNetworkServiceCreate*`）全通过
- [x] `go build ./pkg/adapters/runtime/...` 通过，`make validate-architecture` 通过，`gofmt -l` 改动文件无输出，`git diff --check` 干净
- [x] 镜像 `docker.changqingyun.cn/ani/ani-gateway:dev-20260918-routelb` 构建推送成功（digest `sha256:7d7c9adffa8437dc50d280c9c70776aca7746c1174e963b8e1aa0c9c63169318`）
- [x] 部署至 ani-system（etcd 预检 43% 安全 → `kubectl set image` 未改 env → rollout 成功 → healthz 200）
- [x] 实测 4 项断言全 PASS / 0 FAIL

## 单测覆盖

| 用例 | 覆盖点 |
|---|---|
| `TestLocalNetworkServiceCreateSubnetValidatesVPCViaStore` | store 模式子网创建：校验 SQL 命中 `network_vpcs`、`INSERT INTO network_subnets` 落库 |
| `TestLocalNetworkServiceCreateLoadBalancerValidatesVPCViaStore` | store 模式负载均衡创建：同上，落 `network_load_balancers` |
| `TestLocalNetworkServiceCreateRouteValidatesVPCViaStore` | store 模式路由创建：同上，落 `network_routes` |
| `TestLocalNetworkServiceCreateRejectsMissingVPCViaStore` | 表驱动反向路径：store 无该 VPC（`no rows`）与 VPC state=deleted 两种情形下，三类创建均须 `ErrNotFound` 且零写操作 |

## live 实测证据（ani-system 10.10.1.66:30080）

**前提（保证确实命中"内存 map 为空"路径）**：Pod 于 2026-09-18 10:08 前后随本批次镜像重启；目标 VPC `test-vpc-ly123`（`vpc_68633c00-02ab-4ff2-ac57-11b24784e63f`）创建于 `2026-09-14T05:52:40Z`，只存在于持久层，必不在新进程内存中。

| 断言组 | 关键证据 |
|---|---|
| 历史 VPC 创建路由 | `POST /networks/routes` 404 → **201**，`rt_09519fd9-1231-4299-bd0f-bb88215ddd8a`，且 `dev_profile.mode=real`/`provider=kubeovn`（真实 apply 到 KubeOVN） |
| 历史 VPC 创建负载均衡 | `POST /networks/load-balancers` 404 → **201**，`lb_75a7a7c6-f375-4670-a572-1dd3ad39ee63` |
| 历史 VPC 创建子网（同源缺陷） | `POST /networks/subnets` 404 → **201**，`subnet_2c430138-5808-42d4-9a22-2d369ddcb5dd` |
| 不存在 VPC 仍须 404 | `POST /networks/routes`（`vpc_id=vpc_does_not_exist_…`）→ `404 NOT_FOUND`，消息仍为 `capability resource not found: vpc not found` |
| 列表接口复查 | `GET /networks/routes?limit=10`、`GET /networks/load-balancers?limit=10` 均 200 且含本次创建记录（前端列表页可见） |

原始请求/响应 JSON 逐字记录在 `kjs-study/修复bug/路由与负载均衡问题分析与修复记录.md`「修复实施与实测（2026-09-18）」。

## 备注

- **未覆盖**：deleted VPC 的线上实测（需先造一个可删除 VPC），该分支由单测 `TestLocalNetworkServiceCreateRejectsMissingVPCViaStore/deleted/*` 覆盖。
- **实测残留**：本次原地创建了路由（已真实 apply 到 KubeOVN）、负载均衡、子网三个测试资源，会成为 `test-vpc-ly123` 的关联资源，删除该 VPC 前需先清理。
- **构建方式**：沿用 `OBJECT-STORAGE-BUCKET-FIX-A` 之后确立的"整体覆盖构建机源码树"路径——本地打包 `go.work`/`go.work.sum`/`pkg`/`services/ani-gateway`/`runtimeadmin`（558 项，约 1MB）上传后解包覆盖 `/root/ani-build`，解包前已把构建机旧树整体备份为 `_backup_premerge_routelb.tar.gz`；本地与构建机 `Dockerfile` 已核对一致，不再使用并集构建。
- **ani-test2 未部署**（本批次只部署 ani-system）。
- **流程文档**：本批次为 bug 收口类改动（无新能力边界/API 路径/live gate 定义），按 `CLAUDE.md` §6.3 仍按 Feature batch 更新四文件（本文件 + `README.md` + `CURRENT-SPRINT.md` + `ANI-06-开发计划.md`）。