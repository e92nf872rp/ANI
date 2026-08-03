# CORE-STORAGE-CONSOLE-APIS-BACKEND-A — 存储模块 Console 控制面后端实现

> Batch: CORE-STORAGE-CONSOLE-APIS-BACKEND-A · 产品线: core
> 前置契约：`feat(api): update storage module v1 contract`（上游 PR #71 已合入）

完成日期：2026-07-24
对应 Sprint：Sprint 13 并行存储模块实现切片
验证结果：`go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router ./services/ani-gateway -count=1 -run 'Storage|Bucket|Vector'`、`python scripts/validate_storage_alpha_contract.py`、`python scripts/validate_vector_alpha_contract.py`、`python scripts/validate_component_imports.py` 通过。

## 实现了什么

在存储模块 v1 契约已批准合入后，补齐 Console 所需的存储模块控制面后端闭环（ports → local service → gateway handler），覆盖对象存储桶、块存储卷、文件存储和向量库管理接口：

- `ports.StorageBucketRecord` 扩展 `endpoint`/`acl`/`storage_class`/`versioning`/`lifecycle_rules` 等 Console 字段。
- `ports.StorageService` 新增桶对象列表/删除、前缀创建、预签名 URL、ACL/存储类型更新、生命周期规则 CRUD 接口。
- `ports.StorageService` 补齐卷扩容、挂载/卸载、从快照创建、自动快照策略、OS 初始化指南与完成标记接口。
- `ports.StorageService` 补齐文件系统扩容、mount target 创建、挂载/卸载和 mount command 查询接口。
- `ports.VectorStoreService` 补齐索引重建、知识库关联/解绑、删除预检查接口，并记录向量数量、索引状态和最近索引时间。
- `LocalStorageService` 与 `LocalVectorStoreService` 实现上述 local profile 状态机；Gateway 注册对应路由并完成请求/响应映射。

## 边界

- 不实现 Console/BOSS 前端页面。
- 不实现真实 MinIO/Rook/Milvus provider 新行为；本批次只补齐已批准契约在 local profile 与 Gateway 的控制面闭环。
- 不把 Services 业务语义回流 Core；仅落地 Core OpenAPI 已批准的存储控制面接口。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `pkg/ports/storage_resources.go` | 修改 | 扩展 bucket/volume/filesystem record/request 与 `StorageService` 接口 |
| `pkg/ports/vector_store.go` | 修改 | 扩展 vector store 管理接口与记录字段 |
| `pkg/adapters/runtime/storage_service.go` | 修改 | local profile 实现桶、卷和文件系统管理状态机 |
| `pkg/adapters/runtime/vector_store_service.go` | 修改 | local profile 实现索引重建、KB link 与 delete precheck |
| `services/ani-gateway/internal/router/storage_resources.go` | 修改 | 注册存储 Console 路由与 handler/response mapper |
| `services/ani-gateway/internal/router/vector_store_resources.go` | 修改 | 注册向量库管理路由与 handler/response mapper |
| `*_test.go` | 修改 | 新增 local service、router schema 和后端 HTTP E2E/API 测试 |

## 设计决策

### D1：对象列表采用 prefix 折叠，不返回全量扁平 key
- 与原型对象浏览器一致：根目录先展示 `models/` 等前缀，再进入前缀看对象。
- 避免一次列出整桶大对象集合。

### D2：ACL `tenant_read` 同步映射 `access_mode=public_read`
- OpenAPI 同时存在 `access_mode` 与 `acl`；Console 以 ACL 操作为主。
- local profile 保持两者可读一致，避免列表与详情字段分叉。

### D3：预签名 PUT 允许对象尚未入库
- 上传流程是“先拿 URL 再上传”；GET 仍要求对象已存在。

### D4：卷与文件系统操作只实现 local profile 控制面状态机
- 扩容只允许增大容量；挂载/卸载记录本地状态和历史。
- OS init guide 返回确定性的本地命令，不假装完成 guest 内真实初始化。

### D5：向量库删除预检查只基于 Core 控制面引用
- 已关联知识库时返回不可删除；解绑后返回可删除。
- 不在 Core 增加文本检索或 RAG 业务语义，`/vector-stores/{id}/search` 保持 vector-only。

### D6：新增异步端点返回当前契约允许的 AsyncTask 壳
- 本批次不修改已合入的 OpenAPI 契约；当前 `AsyncTask.task_type` enum 仅包含既有值。
- local profile 已同步更新目标资源，并通过 `result` 返回资源快照；后续如需精确任务类型，应单独走契约 PR 扩展 enum。

## 验证命令

```bash
cd repo
python scripts/validate_storage_alpha_contract.py
python scripts/validate_vector_alpha_contract.py
python scripts/validate_component_imports.py
GOCACHE=/tmp/ani-go-cache GOMODCACHE=/tmp/ani-go-ci-125/pkg/mod \
  go test ./pkg/adapters/runtime ./services/ani-gateway/internal/router ./services/ani-gateway \
  -count=1 -run 'Storage|Bucket|Vector'
```

## 2026-07-27 真实后端 E2E 复验

本次复验使用本地修复版 `ani-gateway` 二进制连接真实集群依赖（auth-service、Postgres、Redis、NATS、MinIO、Milvus 均经 `kubectl port-forward`），不执行前端 E2E。

- 块/文件存储：安装/恢复 external-snapshotter CRD/controller 后，`scripts/validate_storage_live_gate.py --live` 通过，真实 Gateway 控制面 + Rook-Ceph RBD `VolumeSnapshotClass` + PVC create + snapshot create/list + filesystem create + mount-target list 全链路通过；证据：`development-records/live-evidence/storage-console-apis-storage-live-local-gateway-20260727.json`。
- 对象存储：`scripts/validate_object_store_live_gate.py --live` 通过，真实 Gateway 控制面 + MinIO health + bucket create/list + upload/download pre-signed URL 全链路通过；证据：`development-records/live-evidence/storage-console-apis-object-live-local-gateway-20260727.json`。
- 向量库：`scripts/validate_vector_store_live_gate.py --live` 通过，真实 Gateway 控制面 + Milvus collection readiness + vector store create + document insert 全链路通过；证据：`development-records/live-evidence/storage-console-apis-vector-live-local-gateway-20260727.json`。
- 向量库复验中发现并修复 Milvus collection name 缺陷：UUID tenant/store 组合会生成数字开头 collection name，被 Milvus 拒绝；`pkg/adapters/vectorstore/milvus_store.go` 在安全名数字开头时补 `ani_` 前缀，`TestMilvusVectorStoreCollectionNameStartsWithLetterForUUIDTenant` 固定回归。
- 本次块/文件存储复验使用本地 Gateway 连接真实集群依赖；JWT `tid` 与现有 namespace 对齐为 `ani-tenant-11111111-1111-1111-1111-111111111111`。该证据证明后端真实依赖 E2E 通过，不升级为 production-shaped Gateway 结论。

复验命令：

```bash
cd repo
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/vectorstore -count=1
GOCACHE=/tmp/ani-go-cache go test ./pkg/adapters/vectorstore ./pkg/adapters/runtime ./services/ani-gateway/internal/router ./services/ani-gateway -run 'Storage|Bucket|Object|Vector|Milvus' -count=1
PATH=/tmp/ani-pybin:$PATH GOCACHE=/tmp/ani-go-cache make test
PATH=/tmp/ani-pybin:$PATH GOCACHE=/tmp/ani-go-cache make validate-architecture
git diff --check
cd repo && TOKEN=$(cat /tmp/ani-live-token) && python3 scripts/validate_storage_live_gate.py --live \
  --gateway-url http://127.0.0.1:8080/api/v1 \
  --ani-bearer-token "$TOKEN" \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --namespace ani-tenant-11111111-1111-1111-1111-111111111111 \
  --storage-class ani-rbd-ssd \
  --snapshot-class csi-rbdplugin-snapclass \
  --filesystem-backend nfs \
  --evidence-output development-records/live-evidence/storage-console-apis-storage-live-local-gateway-20260727.json
```

## 完工标准

- [x] 契约已合入后再写 ports/handler
- [x] 桶对象 list/delete/upload/prefix/presigned-url 可用
- [x] ACL / storage-class / lifecycle-rules 可用
- [x] 卷 expand/mount/unmount/snapshot-origin/auto-snapshot/os-init 可用
- [x] 文件系统 expand/mount-target/mount/unmount/mount-command 可用
- [x] 向量库 rebuild/KB-link/delete-precheck 可用
- [x] 单测覆盖 local service、response schema 与后端 HTTP E2E/API 路径
- [x] 不包含前端实现
- [x] 对象存储与向量库真实后端 E2E 复验通过
- [x] 块/文件存储真实 snapshot/mount-target E2E 复验通过
