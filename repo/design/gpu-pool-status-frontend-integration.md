# BOSS「GPU 资源池态势」页面 · 前端对接文档

> 契约真实来源：`repo/api/openapi/v1.yaml`（本文所有字段/错误码以其为准，可全局搜索 operationId 定位）。
> 适用分支：ani-hotfix（GPU device surface 批次落地后）。
> 页面角色：BOSS 平台管理员，全部接口使用**平台 token** 调用；base path `https://{host}/api/v1`。

---

## 0. 能力边界（先读，避免做出做不了的交互）

| 能力 | 结论 |
|---|---|
| 行内「切分」（对某一张卡切分） | **不提供**。切分只有集群级统一等分（2/4/8，节点级，one-way）。UI 应提供页面级「集群切分」入口 + 任务进度展示 |
| 行内「分配」（把某张卡分给某租户）/ 设备级预留 | **不提供**。预留是**数量型额度**（`allocated_gpu_count`，不绑定具体卡）。「已预留」统计卡按额度口径展示 |
| 设备状态人工标记 | **提供**：`maintenance`（维护中）/ `unavailable`（不可用）/ `idle`（恢复空闲），可带原因，落平台台账 |
| 自动故障原因（如 Xid 79 错误码） | **无自动采集来源**。`fault` 状态本身是自动观测的，但「原因列/事件流」只承载人工操作与切分任务事件。原因列示例请用人工原因（如「驱动升级窗口」） |

---

## 1. 页面元素 ↔ 接口映射总表

| 页面元素 | 接口 | operationId |
|---|---|---|
| 统计卡：物理卡/逻辑卡、空闲未分配、已占用·覆盖N租户、异常（维护/不可用分项） | `GET /gpu-inventory/occupancy` | `getGPUOccupancy` |
| 统计卡：已预留（额度口径） | `GET /quotas`（聚合各租户 `gpu_reservation`） | `listQuotas` |
| 设备列表（含筛选/分页） | `GET /gpu-inventory` | `listGPUInventory` |
| 行内操作：标记维护 / 标记不可用 / 恢复空闲（带原因） | `PATCH /gpu-inventory/{device_id}` | `updateGPUDeviceStatus` |
| 「集群切分」入口（页面级按钮/弹窗） | `POST /gpu-inventory/gpu-partitions` + `GET /tasks/{task_id}` 轮询 | `createGPUPartition` |
| 联动事件块 | `GET /gpu-inventory/events` | `listGPUDeviceEvents` |

---

## 2. 统计卡

### 2.1 `GET /gpu-inventory/occupancy`

无需参数，一次返回：

```json
{
  "total": 8, "in_use": 3, "available": 3, "fault": 1,
  "by_gpu_type": [ { "gpu_type": "A100", "total": 6, "in_use": 2, "available": 3 } ],
  "vgpu_count": 0, "wholecard_count": 8,
  "physical_card_count": 8,
  "logical_card_count": 0,
  "maintenance_count": 1,
  "unavailable_count": 1,
  "tenant_count": 2,
  "dev_profile": { "...": "..." }
}
```

页面映射：

| 统计卡 | 取值 |
|---|---|
| 物理卡/逻辑卡 `8/0` | `physical_card_count` / `logical_card_count`（逻辑卡 = 整卡数 + vGPU 切片数合计；全整卡时为 0） |
| 空闲未分配 | `available`（副标题「可切分或分配」请去掉「分配」措辞，改为「可切分/可用」） |
| 已占用 · 覆盖 N 个租户 | `in_use` · `tenant_count` |
| 异常 `维护1 · 不可用1` | `maintenance_count` · `unavailable_count`（另 `fault` 为自动检测故障数，可视情况并入异常角标） |

> **零值字段省略**：`maintenance_count` / `unavailable_count` / `tenant_count` / `wholecard_count` / `vgpu_count` 为 0 时响应里**不出现该字段**（omitempty），前端按「缺字段 = 0」处理，不要假定字段总在。

### 2.2 已预留（额度口径）— `GET /quotas`

无跨租户「预留总额」现成字段，由前端聚合：分页拉取 `GET /quotas?limit=200&cursor=...`，对每租户的 `gpu_reservation.allocated_gpu_count` 求和：

```json
{
  "items": [
    {
      "tenant_id": "…", "tenant_name": "demo-corp",
      "items": [ { "resource_type": "gpu_count", "total": 10, "used": 3, "reserved": 0, "available": 7 } ],
      "gpu_reservation": { "tenant_id": "…", "allocated_gpu_count": 4, "used": 2, "reserved": 0, "available": 2 }
    }
  ],
  "next_cursor": "…", "total": 12
}
```

- 统计卡「已预留」= Σ `gpu_reservation.allocated_gpu_count`；可加副标题「额度口径，不绑定具体卡」。
- `gpu_reservation` 为 null/缺省表示该租户未设置预留，按 0 计。
- 「暂无预留设备」这类文案请改为「未设置预留额度」。

---

## 3. 设备列表 — `GET /gpu-inventory`

查询参数（均可选）：`gpu_type`、`gpu_mode=wholecard|vgpu`、`status=available|in_use|fault|maintenance|unavailable`、`node_name`、`limit`（默认 50，最大 200）、`cursor`。

> **分页说明**：`cursor` 参数后端当前忽略，响应 `next_cursor` 恒为 `null`。按 `limit=200` 一次拉全即可（当前集群规模远小于上限）；后续数据量大再做真翻页时契约不变。

响应 `items[]` 字段 → 表格列映射：

| 表格列 | 字段 | 说明 |
|---|---|---|
| 设备 ID | `id` | uuid（由 node_name+gpu_index+型号 派生，稳定可作 row key） |
| 节点 / 设备 | `node_name` / `gpu_index` | 展示为 `gpu-a / GPU-0` |
| 型号 / 显存 | `gpu_type` / `memory_total_mb` | 显存 MiB → GiB 换算 |
| 切分形态 | `gpu_mode` + `shares` + `gpu_sharing_spec` | `wholecard`→「整卡」；`vgpu`→「1/{shares}（{gpu_sharing_spec}）」 |
| 状态 | `status` | 五态徽章，见下 |
| 租户 | `tenant_id` | in_use 时有值；展示名可结合 `GET /quotas` 的 tenant_name 做映射，或暂显短 ID |
| 占用对象 | `instance_id` | nullable |
| 原因 | `reason` | nullable；仅人工操作原因（PATCH 时填写的），fault 自动态无原因 |

状态徽章建议：

| status | 含义 | 来源 | 建议色 |
|---|---|---|---|
| `available` | 空闲未分配 | 自动观测 | 绿 |
| `in_use` | 已占用 | 自动观测 | 蓝 |
| `fault` | 故障 | 自动观测（不可手工设置） | 红 |
| `maintenance` | 维护中 | 人工标记 | 橙 |
| `unavailable` | 不可用 | 人工标记 | 灰/深红 |

注意：`maintenance`/`unavailable`/`reason` 是平台台账覆盖态，**仅平台 token 的清单/占用视图会合并返回**（BOSS 场景始终携带，无需处理）。

---

## 4. 行内操作 — `PATCH /gpu-inventory/{device_id}`

替代原型中的「切分/分配」按钮。建议操作项：`标记维护`、`标记不可用`、`恢复空闲`。

请求：

```
PATCH /api/v1/gpu-inventory/{device_id}
Header: Idempotency-Key: <uuid，每次用户点击生成一次，重试复用>
Body:   { "status": "maintenance", "reason": "驱动升级窗口" }
```

- `status` ∈ `maintenance | unavailable | idle`；`idle` = 清除人工覆盖恢复空闲，此时 `reason` 可省略。
- `fault` 为自动检测态，传它会返回 400。
- `reason` 必填建议：置 `maintenance`/`unavailable` 时前端弹窗要求填写（≤512 字符），持久化进台账并写入联动事件。

响应：`200` 返回更新后的 `GPUInventoryRecord`（已合并台账态），直接替换行数据。
错误：`400 BAD_REQUEST` / `404 DEVICE_NOT_FOUND`；local/dev profile 未配置台账存储时返回 `501 NOT_IMPLEMENTED`。

错误响应统一结构：

```json
{ "code": "DEVICE_NOT_FOUND", "message": "gpu device not found", "request_id": "…" }
```

---

## 5. 集群切分（页面级入口）— `POST /gpu-inventory/gpu-partitions`

交互建议：页面级按钮「集群切分」→ 弹窗选择等分份数（2 / 4 / 8，单选）→ 提交 → 弹窗内轮询任务进度。

```
POST /api/v1/gpu-inventory/gpu-partitions
Header: Idempotency-Key: <uuid，弹窗提交时生成，重试/重提交复用>
Body:   { "shares": 4 }
```

- `202` 受理：body `{ "task_id": "…", "task_type": "gpu_partition", "status": "running", "progress_pct": 0 }`，同时响应头 `Location: /api/v1/tasks/{task_id}`。
- 轮询 `GET /api/v1/tasks/{task_id}` 至 `completed` / `failed`；跳过明细（哪些节点因持有 GPU Pod 被跳过）在任务 `result` 中，建议完成后展示。
- `422`（code 见响应 `NoIdleWholecardGPUs`）：集群内没有可切分的空闲整卡节点——按钮可先按 `occupancy` 预判禁用（无空闲整卡时置灰）。
- 语义提醒：切分对**集群内全部空闲整卡节点统一生效**（跨型号），不是对单张卡；切分后 one-way，无自动回切整卡。切分完成后创建规格走 `POST /gpu-specs`（另行对接，不在本页）。

---

## 6. 联动事件 — `GET /gpu-inventory/events`

参数（均可选）：`device_id`、`event_type=status_changed|partition_applied`、`limit`（默认 50，最大 200）、`cursor`（后端当前忽略，`next_cursor` 恒为 `null`，按 `limit=200` 拉全即可）。按时间倒序。

```json
{
  "items": [
    {
      "id": "…",
      "device_id": "…",          // 节点级事件（集群切分）为 null
      "node_name": "gpu-c",
      "gpu_type": "A10",
      "event_type": "status_changed",
      "reason": "驱动升级窗口",
      "actor": "<平台用户ID或platform>",
      "created_at": "2026-09-11T09:42:00Z"
    }
  ],
  "total": 1, "next_cursor": null, "dev_profile": { "…": "…" }
}
```

- 事件只含人工状态翻转与切分任务两类（`status_changed` / `partition_applied`）；**没有**自动故障事件（「gpu-dev-06 标记为不可用 · Xid 79」这类条目应来自人工标记操作）。
- 展示建议：`标记为不可用 · {reason}（{node_name}）· 时间`；事件无需长轮询，随「刷新」按钮或 30s 定时器重拉即可。

---

## 7. 验收清单

- [ ] 五张统计卡数据全部来自 `occupancy`，「已预留」来自 `GET /quotas` 聚合且标注额度口径
- [ ] 表格无「切分/分配」行内按钮；操作列为 标记维护 / 标记不可用 / 恢复空闲（PATCH + Idempotency-Key + reason 弹窗）
- [ ] 页面级「集群切分」入口：2/4/8 单选 → 202 → 轮询 tasks → 展示跳过明细；422 时按钮禁用或提示
- [ ] 状态徽章五态与 §3 映射一致；`fault` 行无人工操作入口
- [ ] 联动事件块接 `/gpu-inventory/events`，无「自动故障」类假数据
- [ ] 所有写请求重试复用同一 `Idempotency-Key`
