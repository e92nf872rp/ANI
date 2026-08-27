# STORAGE-CONTROL-PLANE-STATE-A

> 日期：2026-08-03
> 范围：ANI Core / 存储与向量控制面状态以 PostgreSQL 为权威（Gateway 重启可恢复）

## 目标

在不改 OpenAPI v1 的前提下，把 volume/filesystem/bucket/object/snapshot/mount-target/vector/KB-link 的控制面身份、状态与墓碑落到 PostgreSQL：

- GET/LIST 直读 Store（PG）
- 真实 Provider create 先写 pending 再 apply 回写
- 真实 storage/vector profile 缺 `DATABASE_URL`、schema 不完整或 PG 不可达时 Gateway fail-closed
- Gateway rollout 后原 ID/关系/幂等可恢复；删除后 API 隐藏、PG 保留墓碑

## 边界

- 不改 `repo/api/openapi/v1.yaml`
- MinIO 仍是对象内容权威；Milvus 仍是 embedding/collection 数据权威
- 不含 Console/BOSS；不声明 full platform production ready
- evidence 禁止 Token、密码、`DATABASE_URL`、预签名 URL

## 交付

| 切片 | 状态 | 说明 |
|---|---|---|
| B1 契约冻结 | done | `test_storage_p0_keeps_existing_v1_without_contract_changes` |
| B2 PG migration | done | `20260803_001_storage_control_plane_state.sql` 已在真实 PG apply；`make validate-storage-control-plane-state` |
| B3 Store/Service 权威 | done | `StorageResourceStore` / `VectorStoreResourceStore`；共享 Store 测试通过 |
| B4 Gateway + live gate | live passed | fail-closed + 真实 Gateway rollout restart / 幂等 / 墓碑已通过；evidence `live-evidence/storage-control-plane-state-live-20260803.json` |

## 契约与脚本

- migration：`deploy/migrations/20260803_001_storage_control_plane_state.sql`
- schema gate：`make validate-storage-control-plane-state`
- live gate：`deploy/real-k8s-lab/storage-control-plane-state-live-gate.yaml`
- live validator：`scripts/validate_storage_control_plane_state_live_gate.py`
- `make validate-storage-control-plane-state-live-gate`

## 验证

```bash
cd repo
go test ./services/ani-gateway/ -count=1 -run 'Storage|VectorStore|ControlPlane'
go test ./pkg/adapters/runtime/ -count=1 -run 'Storage|VectorStore|StoreAuthority'
make validate-storage-control-plane-state
make validate-storage-control-plane-state-live-gate
```

真实 live（需人工确认；Gateway 需已接 `DATABASE_URL` 与 storage/vector 真实 profile；`--subnet-id` 须已存在于 `network_subnets`）：

```bash
cd repo
python3 scripts/validate_storage_control_plane_state_live_gate.py --live --production-shaped --cleanup \
  --gateway-url http://<node>:30080 \
  --ani-bearer-token '<token>' \
  --tenant-id 11111111-1111-1111-1111-111111111111 \
  --subnet-id <existing-subnet-id> \
  --vpc-id <existing-vpc-id> \
  --evidence-output development-records/live-evidence/storage-control-plane-state-live-20260803.json
```

## 当前状态

- B1–B3：已完成
- B4 fail-closed：Gateway runtime 单测已证明 `kubernetes_rest` / `minio` / `milvus` 缺 `DATABASE_URL` 启动失败；schema 缺失拒绝
- B4 live：`production_shape.status=passed`；Gateway `storage-control-plane-state-20260803-v4`；最小 storage/vector 图经 rollout 后按原 ID/list 可回读；volume 幂等 replay/conflict；volume/filesystem/vector soft-delete API 隐藏 + PG 墓碑
- live 修复：List volumes/buckets/vector 避免 pgx nested query `conn busy`；`DeleteFilesystem` 改为 Store-first（对齐 volume）；bucket 无 GET/DELETE by id，live runner 用 list 回读
- 不含 Console / full platform production ready

## 补强：2026-08-27 桶级操作重启后 NOT_FOUND 修复

现场反馈 `GET /api/v1/buckets/{bucket_id}/objects` 在 Gateway 重启后返回 `NOT_FOUND: capability resource not found`：桶已持久化到 PG，但 9 个桶级操作（ListBucketObjects / DeleteBucketObject / CreateBucketPrefix / GenerateBucketObjectPresignedURL / SetStorageBucketACL / SetStorageBucketClass / ListStorageBucketLifecycleRules / CreateStorageBucketLifecycleRule / DeleteStorageBucketLifecycleRule）只查内存 `s.buckets` 缓存，不回退 Store 权威，违背本批次「PG 为权威、重启可恢复」目标。

修复：新增 `LocalStorageService.resolveBucket`（内存缓存→`store.GetBucket` 回退，统一租户/墓碑校验），9 个操作全部改走该解析器；幂等 replay 改为先无锁读 `bucketUpdateIdem` 再经解析器回读，避免持写锁时再取读锁。回归测试：`TestLocalStorageServiceBucketObjectOperationsAfterRestart`（重启后的新服务实例对同一 Store 做 list objects / create prefix / list lifecycle / set ACL）。

验证：`go test ./pkg/adapters/runtime/ -count=1`（除需管理员权限的 Windows 符号链接沙箱测试外全绿）、`go test ./services/ani-gateway/internal/router/ -run 'Storage|Bucket'`、`go vet` 干净。

已知残留（未改，另行处理）：SetStorageBucketACL/Class 与单条生命周期规则增删仅写内存，未回写 Store；重启后这些变更会丢失（桶本身与 ListStorageBucketLifecycleRules 已可恢复）。

### 追加：2026-08-27 可观测性补强（现场反馈发布后仍 404 且无日志）

- `resolveBucket` 未命中时输出 `slog.Warn("storage bucket resolve miss")`，含 tenant_id/bucket_id/reason（memory_miss / store_lookup_miss / memory_record_unusable）/control_plane_store_configured/store_err。
- 9 个桶级操作的 404 消息携带 bucket_id（`capability resource not found: bucket <id> not found`），errors.Is 语义不变。
- Gateway 启动日志新增 `storage provider runtime configured`，暴露 control_plane_store 是否接入（仅当 `STORAGE_PROVIDER=kubernetes_rest` 或 `OBJECT_STORE_PROVIDER=minio` 且配置 `DATABASE_URL` 时才接 PG）。排查要点：若 control_plane_store=false，桶为纯内存态，重启/多副本必丢，需补环境变量或重建桶；若桶在纯内存时期创建，从未写入 PG，需重建。

### 追加：2026-08-27 对象上传确认端点 + 请求访问日志（现场反馈）

现场反馈两个问题：① 前端预签名上传完成后调用 `POST /api/v1/objects/{object_id}/complete` 返回 `404 page not found`（Hertz 默认 NoRoute，该端点从未在契约中定义）；② 运行时无请求日志，无法排查。

契约优先新增（v1 允许增量端点）：
- `api/openapi/v1.yaml`：新增 `POST /objects/{object_id}/complete`（operationId `completeStorageObject`，x-ani-rbac-scope `scope:objects:create`，请求体仅 `idempotency_key`，响应 200/400/401/403/404/412）与 `StorageObjectCompleteRequest` schema。
- `ports.StorageService` 新增 `CompleteStorageObject`；`LocalStorageService` 实现：内存→Store 回退解析对象（租户/墓碑校验），配置对象存储时 `StatObject` 校验内容已上传并回写实际大小/内容类型，未上传返回 `ErrFailedPrecondition`（412），完成后置 `available` 并 `upsertObject` 回写。
- Gateway：`POST /api/v1/objects/:object_id/complete` 路由 + `completeStorageObject` handler + `writeStorageError` 新增 `ErrFailedPrecondition → 412 PRECONDITION_FAILED` 映射；authz 注册表重新生成（drift/路由覆盖校验全绿）。
- 生成链同步：SDK alpha（go/java/python/typescript + metadata）、前端 `core-schema.d.ts`、`core-v1-compatibility-baseline.yaml` 全部重新生成并校验通过；`validate_sdk_alpha.py` 顺带修复 `command_available` 在工具缺失时抛 FileNotFoundError 未兜底的缺陷（现按不可用处理）。

运行时可观测性：
- 新增 `internal/middleware/access_log.go`：每请求一条结构化日志（method/path/status/latency_ms/request_id/tenant_id/user_id），≥500 记 ERROR、≥400 记 WARN、健康探针（/health /ready /healthz /readyz）降为 DEBUG；注册于 RequestID 之后，链路变为 RequestID → AccessLog → TLS → Auth → RBAC → RateLimit → Idempotency → Audit → Route。

测试：服务级 `TestLocalStorageServiceCompleteStorageObject`（未上传→412、上传后→available + 大小回填、未知对象→404）；HTTP 级 `TestStorageHTTPCompleteObjectConfirmsPresignedUpload`（建桶→预签名上传→缺幂等键 400→确认 200→未知对象 404）；中间件 `TestAccessLogEmitsStructuredRequestLines`（200/404/500/健康探针分级断言）。

验证：`go build`/`go vet` 干净；`go test ./services/ani-gateway/... -count=1` 全绿；`go test ./pkg/adapters/runtime/ -count=1`（跳过 Windows 符号链接沙箱测试）全绿；authz drift/路由覆盖/生成测试、SDK alpha、兼容基线校验全部通过。

### 追加：2026-08-27 对象确认 23502 漂移修复 + 处理链路日志（现场反馈）

现场反馈两个问题：① `POST /objects/{object_id}/complete` 报 `upsert storage object: ERROR: null value in column "bucket_id" of relation "storage_objects" violates not-null constraint (SQLSTATE 23502)`；② 请求日志已有，但还需要处理链路日志。

问题①根因：权威迁移 `20260803_001` 中 `storage_objects.bucket_id` 是**可空增量列**（可选 FK 目标），控制面 upsert 从不写该列；但现场库被迁移之外的手动 ALTER 加了 NOT NULL 约束（与先前 `storage_filesystems.storage_class` 同型漂移）。修复：
- 新增幂等迁移 `deploy/migrations/20260827_001_storage_objects_bucket_id_nullable.sql`：`ALTER TABLE storage_objects ALTER COLUMN bucket_id DROP NOT NULL`，干净库上为 no-op；**现场需应用该迁移后重试**。
- 顺带修复相邻缺陷：`CreateStorageObjectUpload` 创建时即 `upsertObject` 持久化，预签名上传在 Gateway 重启后仍可 complete（此前重启会丢对象元数据导致 404）。
- 回归测试：`TestLocalStorageServiceCompleteObjectPersistsToSharedStore`（接共享 Store：建桶→上传→确认→重启实例回读；重启后对旧上传仍可 complete）。

问题②处理链路日志（均为 slog 结构化，含 tenant_id/资源 id）：
- 生命周期 Info：`storage bucket created`（含 object_store/control_plane_store 配置态）、`storage object upload created`（object_id/bucket_id/key/expires_at）、`storage object completed`（size_bytes）、`storage object deleted`。
- 异常 Warn：`storage bucket object store ensure failed`、`storage object complete precondition failed`（412 前置失败原因）、`storage object/bucket control-plane persist failed`（集中在 `upsertObject`/`upsertBucket`，PG 写入失败必留痕）。
- 结合已有 `http request` 访问日志与 `storage bucket resolve miss`，任一现场问题可用「访问日志定位请求 → 链路日志定位环节」两步排查。
