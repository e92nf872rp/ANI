# 平台容量态势接口文档（GET /api/v1/platform/capacity）

> 编制时间：2026-09-03
> 契约真实来源：`repo/api/openapi/v1.yaml`（operationId `getPlatformCapacity`，分支 `feat/platform-capacity` commit `64bc8ab`）
> 用途：BOSS「资源池与容量态势」/「平台资源池总览」整平台只读汇总。

***

## 1. 基本信息

| 项 | 值 |
|---|---|
| 方法 / 路径 | `GET /api/v1/platform/capacity` |
| 鉴权 | Bearer Token（**平台 token**；租户 token 返回 403） |
| RBAC scope | `scope:capacity:read` |
| authz 边界 | `boundary=platform`，`resource=capacity`，`action=get`，`principal_kinds=[user]` |
| 幂等 | 只读接口，无副作用，无需 idempotency_key |
| 分页 | 无（固定 1 条区域记录） |

## 2. 响应结构

### 2.1 200 OK

```jsonc
{
  "regions": [ /* 固定 1 条：整平台 = 默认区域，不做区域 CRUD */ {
    "id": "platform",
    "code": "platform",
    "name": "平台",
    "display_name": "平台（默认区域）",
    "status": "enabled",
    "open_for_tenant": true,
    "azs": ["…"],          // Ready GPU 节点 zone label 去重；缺失为空数组
    "tenant_count": 22,     // 可用租户数（status != disabled）；租户服务不可用时为 0
    "capacity": {
      "gpu_total": 11,      // 全部 GPU 设备数（含 vGPU 切片）
      "gpu_free": 3,        // gpu_total - in_use - fault；in_use = 跨租户 Running GPU Pod 数（每 Pod 占 1 设备）
      "nodes": 3,           // Ready GPU 节点数
      "cpu_cores": 512,     // 节点 allocatable CPU 总量（非可用量）
      "memory_gib": 1760    // 节点 allocatable 内存总量 GiB（非可用量）
    }
  }],
  "summary": {              // regions 的投影（单区域下数值恒等）
    "region_count": 1,
    "gpu_total": 11,
    "gpu_free": 3,
    "tenant_count": 22,
    "nodes": 3,
    "azs": []
  },
  "dev_profile": {
    "mode": "real",                       // real | local
    "provider": "kubernetes-platform-capacity",
    "real_provider": true,                // 全数据源成功才为 true
    "reason": "…"                         // 降级时写明失败的数据源
  }
}
```

### 2.2 错误码

| 状态码 | 场景 | code |
|---|---|---|
| 401 | 无 Authorization 头 / 坏 token | `UNAUTHORIZED` |
| 403 | 租户 token 访问平台边界接口 | `FORBIDDEN` |
| 500 | 服务内部错误（provider 装配异常等） | `PLATFORM_CAPACITY_FAILED` |

> 降级说明：单个数据源（GPUInventory / 节点 / 租户服务）失败**不返回 5xx**，接口仍 200，失败源字段置 0/空数组，`dev_profile.real_provider=false` + `reason` 注明降级原因。

## 3. 数据口径

| 字段 | 口径 |
|---|---|
| gpu_total | GPUInventory `ListNodeClasses` 全部设备数（含 vGPU 切片），含 NotReady 节点设备 |
| gpu_free | `gpu_total − in_use − fault`；in_use 为集群级存在性 label selector（`ani.kubercloud.io/tenant-id`）统计的 Running GPU Pod 数，每 Pod 占 1 设备，与 gpu-inventory occupancy 语义一致；Pod 计数超设备数时截断为设备数，保证 gpu_free ≥ 0 |
| nodes | Ready 的 GPU 节点数 |
| cpu_cores / memory_gib | 节点 allocatable 汇总（总量口径，非可用量） |
| azs | `topology.kubernetes.io/zone` 优先，回退 `failure-domain.beta.kubernetes.io/zone`，去重；缺失为空数组 |
| tenant_count | TenantService 中 status ≠ disabled 的租户数 |

## 4. 调用示例

```bash
# 平台登录
TOK=$(curl -s -X POST http://<gateway>/api/v1/auth/platform/password/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"root","password":"<平台密码>"}' | jq -r .access_token)

# 查询平台容量
curl -s http://<gateway>/api/v1/platform/capacity \
  -H "Authorization: Bearer $TOK" | jq .
```

## 5. 前端接入要点

1. **仅 BOSS 平台端调用**：租户控制台不得调用（租户 token 必 403）。
2. **容忍 azs 为空数组**：节点未打 zone label 时为 `[]`，展示侧需处理空态。
3. **real_provider 降级提示**：`dev_profile.real_provider=false` 时建议展示"数据可能不完整"提示，并用 `reason` 说明。
4. **总量口径标注**：cpu/memory 显示"总量（allocatable）"，避免误解为可用余量。
