# 存储占用标记（in_use / used_by）前端对接文档

> 对应后端批次：`INSTANCE-STORAGE-USAGE-A`（方案见 [instance-storage-usage-a.md](./instance-storage-usage-a.md)）
> 后端拦截批次：`INSTANCE-RWO-PRECHECK-B` 已实施（见第 6 节：创建/挂载/启动命中占用卷返回 409）
> 状态：契约已定稿，后端实施中；本文档所有字段均为 **additive**，不影响现有解析。
> 变更范围：`GET /api/v1/volumes`、`GET /api/v1/volumes/{volume_id}`、`GET /api/v1/filesystems`、`GET /api/v1/filesystems/{filesystem_id}`

---

## 1. 背景

块存储卷（ani-block，RWO）同一时刻只能被一个活跃实例挂载。后端已提供两层能力：占用标记（`in_use`/`used_by`）与 **409 占用预检**（创建/挂载/启动三个入口，见第 6 节）。因此：

- **创建实例时，前端建议用过滤参数排除被占用的卷**——让用户根本选不到冲突卷，体验好于提交后报 409；
- 文件系统（NFS/CephFS，RWX）天然支持多实例共享，**不需要过滤**，占用信息仅作展示。

## 2. 新增字段与参数

### 2.1 列表与详情响应新增字段（卷和文件系统一致）

| 字段 | 类型 | 说明 |
|---|---|---|
| `in_use` | boolean | 是否被活跃实例占用（实例状态为 provisioning/running 时算占用；stopped/deleted 已释放不算） |
| `used_by` | array | 占用/引用该资源的实例列表，可能为空数组 |
| `used_by[].instance_id` | string | 实例 ID |
| `used_by[].instance_name` | string | 实例名 |
| `used_by[].kind` | string | 实例类型：`container` / `vm` / `batch_job` / `sandbox` |
| `used_by[].state` | string | 实例当前状态 |
| `used_by[].mount_path` | string \| null | 挂载点 |

### 2.2 列表接口新增 query 参数

| 参数 | 取值 | 说明 |
|---|---|---|
| `in_use` | `true` / `false` | 按占用状态过滤；**省略时返回全部**（现有行为不变） |

与 `limit`/`cursor` 可组合，分页行为不变。

### 2.3 响应示例

```json
{
  "items": [
    {
      "id": "vol_0b7dbc7f-b13e-42ed-bffe-751b1133438d",
      "name": "test",
      "size_gib": 40,
      "storage_class": "ani-block",
      "state": "available",
      "in_use": true,
      "used_by": [
        {
          "instance_id": "inst_fd237a44-ad2b-4efe-94fd-09f7310a51fb",
          "instance_name": "test-mount-filesystem2",
          "kind": "container",
          "state": "running",
          "mount_path": "/data"
        }
      ]
    }
  ],
  "total": 1,
  "next_cursor": null
}
```

---

## 3. 场景对接

### 场景 1：创建实例弹窗 — 卷下拉（必改）

```
GET /api/v1/volumes?in_use=false&limit=100
```

- 只展示未被占用的卷；用户选不到被占用卷，从源头避免实例卡 provisioning
- `state` 仍需按现有逻辑过滤（如只展示 available），`in_use` 是叠加条件
- 下拉为空时的空态文案建议：「暂无可用卷，请先创建或释放卷」

### 场景 2：创建实例弹窗 — 文件系统下拉（不过滤，仅展示）

```
GET /api/v1/filesystems?limit=100
```

- **不要**加 `in_use=false`——文件系统支持多实例共享
- 可选展示：条目上标注「已被 N 个实例使用」（`used_by.length`），帮助用户了解共享情况，但不得作为禁选条件

### 场景 3：块存储列表页 — 挂载实例列（修正）

- 现状问题：该列目前显示的是 mount name（如 `volume-vol-0b7dbc7f-...`），不是实例
- 改用 `used_by` 渲染：
  - 空数组 → 显示 `-`
  - 有值 → 显示实例名（多个时 `+N` 或 tooltip 展开）；建议实例名可点击跳转实例详情
- 状态 tab（全部/可用/已挂载/异常）如前端本地计算，「已挂载」改用 `in_use === true`

### 场景 4：文件存储列表页

- 「挂载目标数」列可直接改用 `used_by.length`（比现有数值更准）
- 可选：hover 显示 `used_by` 中的实例名列表

### 场景 5：实例详情/状态展示（可选增强）

- 卷详情页展示 `used_by`：让用户能看到"这个卷被谁占用、挂载点是什么"，解释为什么创建实例时选不到它

---

## 4. 注意事项

1. **过渡态（重要）**：后端暂不拦截冲突挂载。前端 `?in_use=false` 过滤是唯一控制手段，卷下拉**必须**接上；不要依赖后端报错兜底。
2. **`in_use` 的翻转时机**：实例 stop（replicas=0）/删除后，卷的占用立即按接口实时计算返回，无异步延迟；但前端列表页若做了缓存，需在实例状态变化后刷新。
3. **文件系统永远不过滤**：`in_use=true` 的文件系统照常可选，过滤属于误伤。
4. **并发窗口**：两个用户同时创建实例挂同一卷，极端情况下前端看到的 `in_use=false` 可能已过期——这是已接受的过渡态，后续后端预检批次（PRECHECK-B）会补 409 拦截，届时前端只需补充错误提示。
5. **重启盲区（已知过渡态）**：实例 A 停止后其卷被新实例 B 占用，A 再次启动/重启时可能因跨节点 Multi-Attach 卡在 provisioning——该路径不经过选卷 UI，前端过滤管不到。后端 PRECHECK-B 会在 start/restart 入口拦截并返回 409（提示占用实例）。在此之前，前端可在实例启动失败的展示中建议用户检查实例挂载卷的占用情况（`GET /volumes/{id}` 看 `used_by`）。
6. 字段为新增，旧版本前端不读这些字段不受影响；灰度期间新老前端可并行。

---

## 5. 后端交付物核对清单（前端可按此验收）

- [x] `/volumes`、`/filesystems` 列表支持 `in_use` query 参数
- [x] 列表与详情响应含 `in_use`、`used_by` 字段
- [x] stopped 实例释放占用后，`in_use` 翻转为 `false`
- [x] 多实例挂载同一文件系统时，`used_by` 返回全部实例
- [x] OpenAPI 契约（`repo/api/openapi/v1.yaml`）与 SDK 生成物同步更新

以上均已在 dev 集群（10.10.1.66）live 验证通过（2026-09-04，镜像 `ani-gateway:dev-20260904-rwo-precheck-b`）。

---

## 6. 后端 409 占用预检已实施（INSTANCE-RWO-PRECHECK-B）

后端拦截已上线，以下三个入口命中"卷被其他活跃实例占用"时返回 **409 Conflict**（`ports.ErrConflict`），错误消息携带占用实例 ID 与状态：

| 入口 | 触发条件 |
|---|---|
| 创建实例（含 VM 系统盘/数据盘、container 卷挂载） | 任一目标卷被其他 pending/provisioning/starting/running/stopping 实例引用 |
| `attach_volume`（实例挂卷） | 目标卷被其他活跃实例引用 |
| `start` / `resume`（启动已停止实例） | 实例自身挂载的任一卷已被其他活跃实例接管（"停机期间卷被抢占"场景） |

响应示例（HTTP 409，live 实测报文）：

```json
{
  "code": "CONFLICT",
  "message": "capability resource conflict: volume \"vol_0b7dbc7f-...\" is occupied by instance \"inst_fd237a44-...\" (running)",
  "request_id": "req_ea43181e-aaec-4f6e-a8d7-9bb83e21eae2"
}
```

前端判定：HTTP 状态码 `409`（或 body `code === "CONFLICT"`）。

前端对接要点：

1. **不必再依赖 `?in_use=false` 做唯一防线**，但过滤仍建议保留（用户体验：让用户根本选不到被占用卷，好过提交后报错）
2. 创建/启动/挂载失败的 409 提示可直接展示 `message`（含占用实例 ID）；如需进一步展示占用者名称，可用 `GET /volumes/{id}` 的 `used_by` 补充
3. 文件系统（RWX）不受 409 影响，多实例共享照常
4. 注意：409 只在"占用者仍处于活跃状态"时返回；占用者 stop/delete 后入口立即放行
