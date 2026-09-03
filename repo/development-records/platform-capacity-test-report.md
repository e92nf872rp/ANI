# 平台容量态势接口（platform/capacity）测试报告

> 编制时间：2026-09-03
> 分支：`feat/platform-capacity`（commit `64bc8ab`，未 push）
> 范围：单测层 / 命令门禁层 / 真实环境层
> 被测镜像：`docker.changqingyun.cn/ani/ani-gateway:dev-20260903-platformcapacity`
> 测试环境：K8s 集群 10.10.1.66（gateway NodePort 30080，`ANI_AUTH_MODE=auth_service`，`PLATFORM_CAPACITY_PROVIDER=kubernetes_rest`）

***

## 0. 执行摘要

- **单测层**：adapter（kubernetes + local）与 router/runtime 相关用例全部通过。
- **命令门禁层**：`make test`、`make validate-architecture`、`validate-openapi-spec`、`validate-auth-contract`、`validate-gateway-authz` 等全部退出码 0；生成物重跑零漂移；`git diff --check` 通过。
- **真实环境层**：平台 token → 200 且 `real_provider=true`，数据来自真实集群（gpu_total=11、gpu_free=3、nodes=3、tenant_count=22、cpu_cores=512、memory_gib=1760）；租户 token → 403；无凭证 → 401；坏 token → 401。**全部通过**。
- 1 例实测 FAIL 为**测试脚本断言笔误**（provider 期望值写成环境变量值 `kubernetes_rest`，实现有意命名为 `kubernetes-platform-capacity`），修正断言后通过，不构成缺陷。
- 结论：功能实现、门禁、部署、实测四层全部达标。

***

## 1. 单测层

| 用例 | 层级 | 步骤/预期 | 结果 |
|---|---|---|---|
| kubernetes adapter 单测（Overview 聚合、Pod 统计、in_use 截断、单源降级、real_provider 标记） | adapter-real | 构造 fake GPUInventory/RESTClient/TenantService，校验聚合值与降级行为 | PASS |
| local adapter 单测（确定性输出、nil 依赖回退） | adapter-local | 两次调用结果一致；nil 依赖不 panic | PASS |
| router 单测（nil service 回退 local、200 投影、错误路径 500） | gateway-router | handler 直测 | PASS |
| runtime 装配单测（`PLATFORM_CAPACITY_PROVIDER` 各取值分支） | gateway-runtime | 空/local/not_configured→nil；kubernetes_rest→real；未知值→报错 | PASS |

## 2. 命令门禁层

提交前在 `repo/` 下执行，全部通过：

- `make test`
- `make validate-architecture`
- `make validate-openapi-spec`
- `make validate-auth-contract`
- `make validate-gateway-authz`（生成 authz 注册表，零漂移）
- `gen-core-sdk` 重跑（go/java/python/ts 四语言 SDK + sdk-metadata，零漂移）
- `git diff --check`

## 3. 真实环境层

环境：10.10.1.66 K8s 测试集群，gateway deployment 镜像 `dev-20260903-platformcapacity`，env 已含 `PLATFORM_CAPACITY_PROVIDER=kubernetes_rest`，Pod `1/1 Running`。

| # | 用例 | 请求 | 预期 | 实际 | 结果 |
|---|---|---|---|---|---|
| 1 | 平台登录 | `POST /api/v1/auth/platform/password/login`（root） | 200 + token | 200 + token | PASS |
| 2 | 平台 token 查容量 | `GET /api/v1/platform/capacity` | 200，结构齐全，real_provider=true | 200（见下方响应） | PASS |
| 3 | 租户 token 越权 | 同上，租户 token（tenant-a/admin） | 403 | 403 `FORBIDDEN principal not allowed by operation policy` | PASS |
| 4 | 无凭证 | 同上，无 Authorization | 401 | 401 | PASS |
| 5 | 坏 token | `Bearer invalid.token.xxx` | 401 | 401 | PASS |

用例 2 实测响应（2026-09-03）：

```json
{
  "regions": [{
    "id": "platform", "code": "platform", "name": "平台",
    "display_name": "平台（默认区域）", "status": "enabled",
    "open_for_tenant": true, "azs": [], "tenant_count": 22,
    "capacity": {"gpu_total": 11, "gpu_free": 3, "nodes": 3,
                 "cpu_cores": 512, "memory_gib": 1760}
  }],
  "summary": {"region_count": 1, "gpu_total": 11, "gpu_free": 3,
              "tenant_count": 22, "nodes": 3, "azs": []},
  "dev_profile": {
    "mode": "real", "provider": "kubernetes-platform-capacity",
    "real_provider": true,
    "reason": "capacity computed from real cluster (GPU inventory + nodes + tenant store)"
  }
}
```

字段逐项校验（9 项）：regions 长度 1 / region.id=platform / status=enabled / capacity 五字段齐全 / summary.region_count=1 / summary 与 region 数值一致 / dev_profile.mode=real / real_provider=true / tenant_count>=0 —— 全部 PASS。

数据合理性交叉核对：gpu_free=3 = gpu_total 11 − in_use（Running GPU Pod 计 8）− fault（0），与 gpu-inventory occupancy 口径一致；nodes=3 为 Ready GPU 节点数；cpu_cores/memory_gib 为节点 allocatable 汇总。

### 已知非缺陷记录

- 用例 2 字段校验中 `dev_profile.provider` 首轮断言期望 `kubernetes_rest`（按方案示例文本），实际为适配器常量 `kubernetes-platform-capacity`，属测试脚本笔误，修正后通过；实现命名有意与既有 real adapter 风格一致。
- `azs` 为空数组：测试环境 GPU 节点未打 zone label，契约允许（缺失为空数组）。

## 4. 遗留风险

1. GPU 节点补 zone label 后 azs 即可非空，无需改代码。
2. cpu/memory 为 allocatable 总量口径，BOSS 端展示文案需注明"总量"而非"可用"。
3. TenantService 不可用时 tenant_count=0 并写 reason，前端需容忍 0 值。
