# 前端对接提示词：Images 上传 + ISO 装机 VM + noVNC

把下面整段复制给前端/产品 AI 或工程师，用于对接 Core OpenAPI（本仓库不改 `frontends/`）。

---

你在对接 ANI Core REST API（`https://{host}/api/v1`），实现「上传本地 ISO → 创建用该 ISO 装机的虚拟机 → 打开 noVNC 安装界面」。不要调用 Services API（`/api/v1/svc`）。契约以 Core OpenAPI `v1.yaml` 为准。

## 鉴权

- 所有 Gateway 请求带 `Authorization: Bearer <access_token>`。
- 有副作用的 POST 必须带幂等：请求头 `Idempotency-Key` 与 body 内 `idempotency_key` 使用同一值；重试必须复用同一 key。

## 1. 创建镜像上传会话

`POST /api/v1/images/uploads`

```json
{
  "idempotency_key": "img-upload-<uuid>",
  "name": "ubuntu-22.04-live-server",
  "format": "iso",
  "size_gib": 5,
  "content_type": "application/x-iso9660-image"
}
```

要点：

- 当前只支持 `format: "iso"`；`qcow2`/`raw` 会失败。
- **不要传 `storage_class`**，除非用户明确选择；省略后由集群默认 StorageClass 承接。
- `size_gib` 按 ISO 文件大小向上取整并留余量（例如 2.5Gi 文件用 `3` 或 `5`）。

响应（`ImageUploadSession`）关键：

- `image.id`：后续轮询与创建 VM 用的 `image_id`
- `upload_url`：浏览器/客户端直传地址（CDI uploadproxy，常为 `https://<node>:31001/v1beta1/upload`）
- `token`：短期上传票据
- `method`：通常为 `POST`
- `expires_at`：过期后需重新创建会话

## 2. 直传 ISO 文件（不经 Gateway 中转）

对 `upload_url` 发起上传（与会话 `method` 一致，一般为 POST）：

- Header：`Authorization: Bearer <token>`（会话返回的 token，不是用户 JWT）
- Body：ISO 原始二进制（`Content-Type: application/octet-stream` 或 ISO MIME）
- uploadproxy 常为自签证书：开发环境允许 insecure TLS；生产需正确信任链

上传前可轮询 `GET /api/v1/images/{image_id}`，等到适合上传的状态（实现侧 DV 进入 UploadReady 后再传更稳）。

## 3. 轮询镜像就绪

`GET /api/v1/images/{image_id}`

关注 `state`：

- `pending` / `uploading` / `processing` → 继续轮询
- `ready` → 可创建 VM
- `failed` → 展示 `reason`/`message`，允许用户重试（新 idempotency_key）

列表：`GET /api/v1/images`（可按 format/state 过滤；分页字段可能尚未完整生效）。

删除：`DELETE /api/v1/images/{image_id}`（若仍被 VM 引用，契约写 409，但当前实现可能尚未做占用检查——前端删除前最好先确认无实例引用）。

## 4. 用 ISO 创建虚拟机

`POST /api/v1/instances`（字段名以 OpenAPI `CreateInstanceRequest` 为准）

ISO 装机路径（与 `boot_image` 互斥）：

```json
{
  "idempotency_key": "vm-iso-<uuid>",
  "name": "vm-ubuntu-install",
  "type": "vm",
  "cpu": "4",
  "memory": "8Gi",
  "boot_media": {
    "type": "iso",
    "image_id": "<上一步 ready 的 image.id>"
  },
  "root_disk_size_gib": 40
}
```

要点：

- `boot_media.type` 必须为 `iso`；`image_id` 必须是 **Ready** 的 Image。
- `root_disk_size_gib`：空白系统盘大小；未传时服务端可能默认 40。
- 不要同时传 `boot_image`（containerDisk 路径）与 `boot_media`。
- `boot_media.type=disk_image` 当前不支持。

创建成功后轮询实例状态至 Running（或可开控制台的状态）。

## 5. 打开安装界面（noVNC）

`POST /api/v1/instances/{instance_id}/actions/console`（或 OpenAPI 中等价 console action）

响应里的 `connect_url` / `url` 交给前端 noVNC 组件打开（WebSocket 经 Gateway 代理）。用户在 VNC 里完成 OS 安装。

## UI 建议流程

1. 镜像库：选择本地 `.iso` → 创建上传会话 → 进度条直传 → 列表展示 state。
2. 创建 VM：类型选「ISO 安装」→ 下拉选 Ready 镜像 → 填系统盘大小 → 创建。
3. 实例详情：Running 后显示「打开控制台」→ 嵌入/新开 noVNC。

## 不要做的事

- 不要把 ISO 文件 POST 到 Gateway `/images` 请求体（大文件必须走 `upload_url`）。
- 不要写死 StorageClass 名（如 `ani-rbd-ssd`）；默认留空。
- 不要绕过 Core 去调 KubeVirt/CDI CR。
- 不要依赖 Services 层 API。

## 联调环境提示（isolated）

- Gateway：`http://<node-ip>:30080`
- CDI uploadproxy：`https://<node-ip>:31001`
- 部署后 Gateway 应有 `IMAGE_IMPORT_PROVIDER=cdi_rest` 与正确的 `CDI_UPLOADPROXY_URL` / `INSTANCE_CONSOLE_BASE_URL`
---
