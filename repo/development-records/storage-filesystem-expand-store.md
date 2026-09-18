# STORAGE-FILESYSTEM-EXPAND-STORE-A — 文件存储扩容 store 化与 capacity 过渡别名

完成日期：2026-09-18
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
分支：`fix/routing-loadbalancer-bugs`
验证结果：本批次相关 `go test` 全通过，`go vet` 通过，`gofmt -l` 改动文件无输出；K8s 测试环境 ani-system（10.10.1.66:30080）实测 5 项断言全 PASS

## 实现了什么

收口文件存储扩容 `POST /api/v1/filesystems/{filesystem_id}/expand` 对"网关重启前创建"的历史文件存储一律 `404 capability resource not found` 的缺陷（来源 `kjs-study/修复bug/文件存储扩容问题分析与修复记录.md`，用例 文件存储-4，未入库）。

真实根因二层：

1. **(主) 后端 memory-only 校验**：`LocalStorageService.ExpandFilesystem` 只查内存 map `s.filesystems`，无 store 回退；网关以 store 模式（`DATABASE_URL`）运行时进程重启后内存 map 为空，DB 中的历史文件存储被误判为不存在 → 404。这是 storage 模块**最后一个 memory-only 漏网点**——同族 `GetFilesystem`/`DeleteFilesystem`/`CreateFilesystemMountTarget`/`MountFilesystem`/`UnmountFilesystem` 均已走 store 或 `lookupFilesystemRecord` 回退。
2. **(次) 前端字段名漂移**：Console 扩容请求体发 `{"capacity": N}`，而契约（[v1.yaml `StorageFilesystemExpandRequest`](file:///e:/go/project/ANI/repo/api/openapi/v1.yaml)）与 handler 请求结构体只声明 `size_gib`，字段被 `BindJSON` 静默忽略（`size_gib=0`），即使修好 404 也会再撞容量校验。

修复效果（ani-system 实测）：历史文件存储扩容 `404 → 202` 且 `size_gib` 回显新值；容量未增长 `400` 语义保留；不存在的文件存储仍 `404`。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/adapters/runtime/storage_service.go` | 修改 | `ExpandFilesystem`（908-937）查找逻辑由 `s.filesystems[...]` 直查改为复用 `lookupFilesystemRecord`（483-510：内存命中且租户匹配直接用；未命中 store 模式回退 `store.GetFilesystem` 并回填内存缓存） |
| `pkg/adapters/runtime/storage_store_test.go` | 修改 | 新增 2 个 store 模式回归用例（重启后仅库中存在 → 扩容成功且落库；不存在 → `ErrNotFound`、不增长 → `size_gib must be greater`） |
| `services/ani-gateway/internal/router/storage_resources.go` | 修改 | `storageFilesystemExpandRequest` 新增过渡别名 `Capacity`（97-104）；`expandFilesystem`（904-921）在 `SizeGiB==0 && Capacity>0` 时取别名，`size_gib` 优先 |
| `services/ani-gateway/internal/router/storage_resources_test.go` | 修改 | 新增 `TestStorageHTTPFilesystemExpandAcceptsCapacityAlias`：`capacity` 生效、`size_gib` 优先 |

**无 OpenAPI 契约变更、无 SDK/生成物变更、无 DB 迁移。**

## 缺陷与修法

| # | 现象 | 根因 | 修法 |
|---|---|---|---|
| 文件存储-4（后端主因） | 历史文件存储扩容 `404 capability resource not found`（列表 `GET /filesystems` 200 正常可见） | `ExpandFilesystem` 只查内存 `s.filesystems`，store 模式重启后为空 | 改复用 `lookupFilesystemRecord`（store 优先查持久层并回填缓存），与同族方法对齐 |
| 文件存储-4（前端次因） | 请求体 `{"capacity": N}` 被静默忽略 | handler 请求结构体与契约只认 `size_gib` | handler 实现级过渡 shim：新增 `Capacity` 别名，`size_gib` 优先；契约/SDK 不动 |

service 改动：

```go
record, found, err := s.lookupFilesystemRecord(ctx, request.TenantID, request.FilesystemID)
if err != nil {
    return ports.StorageFilesystemRecord{}, err
}
if !found {
    return ports.StorageFilesystemRecord{}, ports.ErrNotFound
}
```

handler 改动：

```go
if req.SizeGiB == 0 && req.Capacity > 0 {
    req.SizeGiB = req.Capacity
}
```

设计说明：

- 幂等检查 `s.fsOpIdem[...]` 仍留在内存（与 `MountFilesystem`/`UnmountFilesystem` 一致），本次未改动，不引入新的持久化面。
- `lookupFilesystemRecord` 内部已承担租户归属校验（store 分支 SQL 按 `tenant_id` 过滤、内存分支显式比对），跨租户仍返回 `ErrNotFound`。
- `capacity` shim 仅作用于文件存储扩容 handler，未扩散到批量扩容等其它入口。

## 完工标准达成

- [x] 本批次相关 `go test`（`TestLocalStorageServiceExpandFilesystem*`、`TestStorageHTTPFilesystemExpandAcceptsCapacityAlias`）全通过
- [x] `go vet ./internal/router/` 通过，`gofmt -l` 改动文件无输出
- [x] 镜像 `docker.changqingyun.cn/ani/ani-gateway:fix-20260918-fsexpand` 构建推送成功（digest `sha256:6b74374d360a3e23ceaadf13e2fbda879de801b3bab6c34f733bee81cb900d9f`）
- [x] 部署至 ani-system（`kubectl set image` 未改 env → rollout 成功 → healthz 200）
- [x] 实测 5 项断言全 PASS / 0 FAIL

## 单测覆盖

| 用例 | 覆盖点 |
|---|---|
| `TestLocalStorageServiceExpandFilesystemAfterRestart` | store 模式重启后（内存空、记录只在库）扩容成功，返回与落库 `size_gib` 均为新值 |
| `TestLocalStorageServiceExpandFilesystemRejectsViaStore` | store 模式反向路径：不存在的文件存储 → `ErrNotFound`；容量未增长 → `size_gib must be greater` |
| `TestStorageHTTPFilesystemExpandAcceptsCapacityAlias` | HTTP 层：`capacity` 别名生效且回显 `size_gib`；同时给 `size_gib` 与 `capacity` 时 `size_gib` 优先 |

## live 实测证据（ani-system 10.10.1.66:30080）

**前提（保证确实命中"内存 map 为空"路径）**：Pod 随本批次镜像重启（`ani-gateway-79b66474f5-cdg9h`），内存 `s.filesystems` 为空；目标文件存储 `fs_f600cbd5-bc90-40ce-b216-2e2ec57da6b0`（`test-ly-nfs`，创建于 `2026-09-14T08:07:57Z`）只存在于持久层。列表 `GET /filesystems?limit=100` → 200、13 条。

| 断言组 | 关键证据 |
|---|---|
| 历史文件存储扩容（`capacity` 别名） | `POST /filesystems/fs_f600cbd5…/expand` 404 → **202**，`result.filesystem.size_gib=101`、`reason=expanded by local storage profile` |
| 规范字段 `size_gib` | 再扩至 102 → **202**，`size_gib=102` |
| 容量未增长 | 同容量请求 → `400 BAD_REQUEST`，`size_gib must be greater than current filesystem size` |
| 不存在的文件存储 | `POST /filesystems/fs_does_not_exist/expand` → `404 NOT_FOUND`，消息仍为 `capability resource not found` |
| 回读落库 | `GET /filesystems/fs_f600cbd5…` → 200，`size_gib=102` |

原始请求/响应 JSON 逐字记录在 `kjs-study/修复bug/文件存储扩容问题分析与修复记录.md`「实测记录」。

## 备注

- **技术债留痕**：`capacity` 为 handler 实现级过渡别名，契约仍只声明 `size_gib`，属已知的轻量契约/实现漂移（代码注释 + 本文 + bug 文档三处留痕），前端切 `size_gib` 后应移除 shim。前端改造点：Console 文件存储扩容请求体字段 `capacity` → `size_gib`。
- **实测残留**：目标文件存储 `test-ly-nfs` 容量被实测从 100 GiB 扩到 102 GiB（测试环境原地变更，未回滚，符合扩容不可逆语义）。
- **构建方式**：沿用"整体覆盖构建机源码树"路径——本地打包 `go.work`/`go.work.sum`/`pkg`/`services/ani-gateway` 上传后解包覆盖 `/root/ani-build`，`nohup` 后台构建 + 以 `digest:` 行/`docker images` 判断完成（构建含缓存，数分钟内完成）。
- **ani-test2 未部署**（本批次只部署 ani-system）。
- **流程文档**：本批次为 bug 收口类改动（无新能力边界/API 路径/live gate 定义），按 `CLAUDE.md` §6.3 仍按 Feature batch 更新四文件（本文件 + `README.md` + `CURRENT-SPRINT.md` + `ANI-06-开发计划.md`）。