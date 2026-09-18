# CORE-STORAGE-BUCKET-DELETE-A — 对象存储桶删除接口（DELETE /buckets/{bucket_id}）

完成日期：2026-09-18
对应 Sprint：Sprint 13（Core real provider 与 live gate 收敛）
分支：`feat/storage-bucket-delete`（基于 main `114e15c`）
方案来源：`kjs-study/对象存储桶删除/对象存储桶删除接口方案.md`（未入库）
验证结果：`make validate-storage-alpha` / `validate-core-api-compatibility` / `validate-gateway-authz` / `validate-doc-api` / live gate 契约与单测（8 passed）全绿；ani-system 实测 **24 项断言全 PASS / 0 FAIL**（1 项 SKIP 见 §7.2）

## 1. 实现了什么

新增 Core 对象存储「删除存储桶」能力 `DELETE /api/v1/buckets/{bucket_id}`（operationId `deleteStorageBucket`，scope 复用 `scope:objects:delete`）。

语义：

- 控制面**墓碑软删**（`state=deleted` + `deleted_at`），零数据库迁移（复用 `UpsertBucket` 既有墓碑分支）。
- 桶内仍有该租户活跃对象 → **409 CONFLICT**，要求先清空对象（对齐 S3 `BucketNotEmpty`）。
- 删除后桶从列表消失，全部桶级子操作（对象列表/上传/ACL/存储类型/生命周期规则）统一 **404**。
- 重复删除 → 404（墓碑被 `resolveBucket` 过滤，天然幂等）。
- **不回收物理 MinIO 桶**：物理桶名 = `bucketPrefix + 桶名`，对象 key = `<物理桶>/<tenant_id>/<object_key>`，物理桶为跨租户共享底座，直接删会毁掉其他租户数据。
- 软删后同名可重建，得到新 `bucket_id`（偏唯一索引 `WHERE deleted_at IS NULL`）。

同时补全 Sprint 13 live gate 的清理闭环（此前 live gate 每跑一次残留一个桶，见方案 §1.5）。

## 2. 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/openapi/v1.yaml` | 修改 | `/buckets` 之后新增 `/buckets/{bucket_id}` 的 `delete`（operationId `deleteStorageBucket`、`x-ani-rbac-scope: scope:objects:delete`、响应 200/401/403/404/409） |
| `pkg/ports/storage_resources.go` | 修改 | `StorageService` 接口新增 `DeleteStorageBucket(ctx, StorageResourceGetRequest) (StorageBucketRecord, error)`（复用既有请求结构体，无新类型） |
| `pkg/adapters/runtime/storage_service.go` | 修改 | 新增 `DeleteStorageBucket` + `bucketActiveObjectCount`；`DeleteBucketObject` 补对象墓碑落盘（关联缺陷，见 §4） |
| `services/ani-gateway/internal/router/storage_resources.go` | 修改 | 注册 `v1.DELETE("/buckets/:bucket_id", api.deleteStorageBucket)`（置于 `/buckets/:bucket_id/objects` 之前）+ handler（错误走 `writeStorageError`，成功 200 返回 `storageBucketFromRecord`） |
| `scripts/validate_storage_alpha_contract.py` | 修改 | `EXPECTED_PATHS` 新增该路径；修复 router 注册函数 token 检查陈旧缺陷（见 §4） |
| `Makefile` | 修改 | `validate-storage-alpha` 的 go test 过滤正则追加 `TestStorageHTTP`，使新增 router HTTP 测试纳入门禁 |
| `pkg/adapters/runtime/storage_service_store_authority_test.go` | 修改 | 新增 2 用例（生命周期全链、底座用量口径） |
| `pkg/adapters/runtime/storage_store_test.go` | 修改 | 新增 `TestMetadataStorageStoreUpsertTombstones`（桶/对象墓碑落盘断言） |
| `pkg/adapters/runtime/plan_audit_store_test.go` | 修改 | `fakeMetadataTx` 新增 `execArgs` 快照（测试基建） |
| `services/ani-gateway/internal/router/storage_resources_test.go` | 修改 | 新增 `TestStorageHTTPDeleteBucketLifecycle` |
| `scripts/validate_object_store_live_gate.py` | 修改 | `REQUIRED_CHECKS` 增 `core-bucket-delete`；cleanup 段增 `DELETE /buckets/{bucket_id}`；evidence 增 `bucket_delete_status` |
| `scripts/validate_object_store_live_gate_test.py` | 修改 | fake requester 补该 DELETE 分支 + `bucket_delete_status` 断言 |
| `deploy/real-k8s-lab/object-store-live-gate.yaml` | 修改 | 新增 `core-bucket-delete` 检查项 |

生成物（由 OpenAPI 派生，全部重生成）：`api/core-v1-compatibility-baseline.yaml`（249→250 operations）、`services/ani-gateway/internal/authz/zz_generated_core_policies.go`、`docs/api/core.html` + `docs/api/index.html`、`sdks/core/{go,python,java,typescript}` + `sdks/core/sdk-metadata.json`。

无 DB 迁移、无 port 新能力（`pkg/ports/object_store.go` 未改动）。

## 3. 方案开放决策落点（D1~D6）

| # | 决策 | 落点 |
|---|---|---|
| D1 | 非空桶处置 | 采用 A：拒绝并返回 409，不做 `force` 级联清空 |
| D2 | 物理桶回收 | 采用 A：不动物理桶，登记遗留项 |
| D3 | 是否顺带暴露 `GET /buckets/{bucket_id}` | 否，不在本批次范围 |
| D4 | 新 operation 鉴权元数据 | 沿用 legacy（不声明 `x-ani-authz`），与 `/buckets` 族其余 13 条一致 |
| D5 | live gate 清理闭环 | 纳入本批次（已落地并验证） |
| D6 | 前端删除入口 | 需 Console 侧确认，本批次不做前端 |

## 4. 实施中发现的偏离与关联缺陷

| # | 项 | 说明 |
|---|---|---|
| 1 | **偏离方案 §4.1 第 6 步（内存保留墓碑）** | 方案原写「回写内存缓存 `s.buckets[bucketID]`」。实现改为**内存不保留墓碑**（`delete(s.buckets, bucket.BucketID)`）：创建路径的重名检查与创建幂等键查表都以 `s.buckets` 为来源，保留墓碑会让同名重建被误判冲突或复用旧记录，与「软删后同名可重建」目标冲突。代码中已注释理由，实测（§6 第 9 步）证明重建正常 |
| 2 | **关联缺陷：`DeleteBucketObject` 墓碑不落盘** | 删对象只改内存并回填 `s.objects`，未写 store。网关重启后 `hydrateObjectsFromStore` 会把已删对象当活跃对象，使桶永远无法通过非空判定删除。已修复：删除时补 `upsertObject` 落盘并同步 `DeletedAt` |
| 3 | **关联缺陷：storage alpha 门禁 router 注册检查陈旧** | `validate_storage_alpha_contract.py` 只接受 `registerStorageResources(v1)` / `registerStorageResourcesWithService(v1, options.StorageService)` 两个字面量，而 main 上 router.go 实为 `registerStorageResourcesWithServiceAndTasksAndStore(...)`（#151/#160/#162/#163 演进后未同步）→ 该门禁在 main 上本就失败。修复：改按公共前缀 `registerStorageResourcesWithService` 匹配 |
| 4 | 非空判定双口径 | `objectStore != nil` 时以底座 `BucketUsage(ObjectCount)` 为准；底座查询失败 → `slog.Warn` + 回退控制面对象统计，**失败不放行**（与 `enrichBucketUsage` 容错风格一致但方向相反） |

## 5. 门禁与单测

已通过：

- `make validate-storage-alpha`（Python 契约 + 全部 go test，含新增 `TestStorageHTTPDeleteBucketLifecycle`）
- `make validate-core-api-compatibility`、`make validate-gateway-authz`（324 注册路由 / 250 registry / 0 error）、`make validate-doc-api`
- `python scripts/validate_object_store_live_gate.py`（contract valid）+ live gate 单测 `8 passed`
- 定向单测：`TestLocalStorageServiceDeleteBucketLifecycle`、`TestLocalStorageServiceDeleteBucketHonorsObjectStoreUsage`、`TestMetadataStorageStoreUpsertTombstones`、`TestStorageHTTPDeleteBucketLifecycle`

单测覆盖：非空 409 且记录不变 → 删对象 → 重启实例后再删成功（store 权威路径）→ 墓碑落盘断言 → 列表 0 条 → 重复删除 404 → 同名重建得新 `bucket_id` → **跨租户 404**；底座用量口径（`ObjectCount=2` → 409，清零后放行）。

> 说明：`make validate-sdk-alpha` / `make validate-core-beta` / `make validate-openapi-spec` / `make test` / `make validate-architecture` / `git diff --check` 按用户指示留到提交前统一执行，不在本记录中声明通过。

## 6. live 实测证据（ani-system 10.10.1.66:30080）

部署：镜像 `docker.changqingyun.cn/ani/ani-gateway:dev-20260918-bucketdel`（digest `sha256:0706a689101180837a66a4002eafde56014a4dfab9195f8aaab3dce457ca16eb`）；部署前镜像 `fix-20260918-vclusteroci`；etcd dbSize 912990208 / 配额 2147483648（约 43%，安全）；`kubectl set image -n ani-system` **未改任何 env**；rollout 成功，Pod `ani-gateway-79b98585c-rlcc9` 1/1 Running。

以下为原始请求/响应（不截断）。

### 6.0 登录

```
=== login tenant-a -> HTTP 200
{
  "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCIsImtpZCI6IiJ9.eyJzdWIiOiIzNDdiNWQ3My0wZWYwLTQ2YWYtOWQ3ZS0zNmRhNTY0YjVkZmYiLCJpc3MiOiJhbmktYXV0aC1zZXJ2aWNlIiwiZXhwIjoxNzg5NzI2MjcxLCJuYmYiOjE3ODk3MjI2NzEsImlhdCI6MTc4OTcyMjY3MSwianRpIjoiNjQwMmUwOTItZjk3My00ZjViLTgwZWMtNTc2ZDNkMzU4NzJmIiwicHJpbmNpcGFsX2tpbmQiOiJ1c2VyIiwidGlkIjoiMDAwMDAwMDAtMDAwMC0wMDAwLTAwMDAtMDAwMDAwMDAwMDAxIiwidWlkIjoiMzQ3YjVkNzMtMGVmMC00NmFmLTlkN2UtMzZkYTU2NGI1ZGZmIiwiY3JlZGVudGlhbF9kb21haW4iOiJ0ZW5hbnQiLCJzY29wZSI6InRlbmFudCIsInJvbGVzIjpbInRlbmFudC1hZG1pbiJdfQ.vHZnKfWY6BiRve_wAkM-FwZRgii58-zNvq_rdlO4bbXgjZAxqgK2okmJXjJ0tplXbBHQRCjL8Ky1KPnHHn3KkZbBfBn_GMROCVF1UH5ZyXVkuyzd5MIa-qbdDmc8O7cSVo7tX0StP2mfcjbh2tS7GhMPLmCarc1fvPC9vLVIsyk0YC2z5i8Lo4MZe1KpUVxUsAyVkrb89_K-siibdi6Cvpr5cC8CNpZSPbIqI2NsoqqNqlMUDQhp41KsV0aYf1PuljM7GJtMa3Wud3l7h81Z9xa6sbNuUyfif3hawh3FPmyudjSlvWA7QYiiFIBKWcl246us7QxtmwmyGQECTiJAfA",
  "expires_in": 3600,
  "issued_at": "2026-09-18T09:11:11Z",
  "refresh_token": "ani_refresh_dH6m63Ys_EHg4gwekkOSgoQYgil9KOFn4k-jaucgsws"
}
=== login tenant-b -> HTTP 404
{
  "code": "TENANT_NOT_FOUND",
  "message": "tenant not found",
  "request_id": "req_d992c563-3052-4056-8ac9-01f87d769ee0"
}
```

### 6.1 建桶

```
=== 1 POST /api/v1/buckets -> HTTP 201
{
  "access_mode": "private",
  "acl": "private",
  "acl_label": "私有",
  "created_at": "2026-09-18T09:11:41Z",
  "endpoint": "https://s3.cn-east-1.ani.local",
  "id": "4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8",
  "lifecycle_note": "未配置生命周期规则",
  "name": "delprobe-1789722701",
  "object_count": 0,
  "region": "cn-east-1",
  "size_bytes": 0,
  "storage_class": "standard",
  "updated_at": "2026-09-18T09:11:41Z",
  "versioning": "disabled"
}
[PASS] 1.create_http_201 http=201
[PASS] 1.echo_id id='4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8'
[PASS] 1.echo_name name='delprobe-1789722701'
```

### 6.2 桶内放对象（制造非空）

```
=== 2a POST /api/v1/buckets/{id}/objects/upload -> HTTP 200
{
  "expires_at": "2026-09-18T10:11:41Z",
  "object_id": "60019c4a-dcd7-4222-b1a5-c26a0cdc20d2",
  "upload_url": "http://10.10.1.66:30900/ani-s13-delprobe-1789722701/00000000-0000-0000-0000-000000000001/delprobe/keep-1789722701.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=ani-s05-minio%2F20260918%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20260918T091141Z&X-Amz-Expires=3600&X-Amz-Signature=22b634aee7169b2534ac2aaa523122144b0e4e3318a22f54a990a625ef15e386&X-Amz-SignedHeaders=host"
}
[PASS] 2a.presign_http_200 http=200
[PASS] 2a.object_id_present object_id='60019c4a-dcd7-4222-b1a5-c26a0cdc20d2'
[PASS] 2b.presigned_put http=200
=== 2c GET /api/v1/buckets/{id}/objects -> HTTP 200
{
  "items": [
    {
      "key": "delprobe/",
      "kind": "prefix",
      "name": "delprobe/"
    }
  ],
  "next_cursor": null,
  "prefix": "/",
  "total": 1
}
[PASS] 2c.list_objects_http_200 http=200
[PASS] 2c.object_id_resolved object_id='60019c4a-dcd7-4222-b1a5-c26a0cdc20d2'
```

### 6.3 非空桶删桶 → 409

```
=== 3 DELETE /api/v1/buckets/{id}（桶内有对象） -> HTTP 409
{
  "code": "CONFLICT",
  "message": "capability resource conflict: bucket 4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8 still holds 1 object(s), delete them first",
  "request_id": "req_c9f1ee2c-8756-4008-b28f-fbf337490320"
}
[PASS] 3.nonempty_http_409 http=409
[PASS] 3.nonempty_code_conflict code='CONFLICT'
[PASS] 3.bucket_still_listed count=1
[PASS] 3.bucket_record_unchanged name='delprobe-1789722701'
```

### 6.4 删对象

```
=== 4 DELETE /api/v1/objects/{object_id} -> HTTP 200
{
  "bucket": "delprobe-1789722701",
  "content_type": "text/plain",
  "created_at": "2026-09-18T09:11:41Z",
  "dev_profile": {
    "mode": "local",
    "provider": "local-storage-service",
    "real_provider": false,
    "reason": "Core dev/local profile; provider execution is gated separately"
  },
  "id": "60019c4a-dcd7-4222-b1a5-c26a0cdc20d2",
  "key": "delprobe/keep-1789722701.txt",
  "reason": "deleted by local storage profile",
  "size_bytes": 5,
  "state": "deleted",
  "tenant_id": "00000000-0000-0000-0000-000000000001",
  "updated_at": "2026-09-18T09:11:42Z"
}
[PASS] 4.delete_object_http_200 http=200
```

### 6.5 空桶删桶 → 200

```
=== 5 DELETE /api/v1/buckets/{id}（桶已空） -> HTTP 200
{
  "access_mode": "private",
  "acl": "private",
  "acl_label": "私有",
  "created_at": "2026-09-18T09:11:41Z",
  "endpoint": "https://s3.cn-east-1.ani.local",
  "id": "4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8",
  "lifecycle_note": "未配置生命周期规则",
  "name": "delprobe-1789722701",
  "object_count": 0,
  "region": "cn-east-1",
  "size_bytes": 0,
  "storage_class": "standard",
  "updated_at": "2026-09-18T09:11:42Z",
  "versioning": "disabled"
}
[PASS] 5.empty_http_200 http=200
[PASS] 5.echo_id id='4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8'
[PASS] 5.echo_name name='delprobe-1789722701'
```

### 6.6 列表校验（已删桶不可见）

```
=== 6 GET /api/v1/buckets（列表校验） -> HTTP 200
{
  "items": [
    {
      "access_mode": "public_read",
      "acl": "tenant_read",
      "acl_label": "租户内读",
      "created_at": "2026-09-17T11:39:59Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "2457f77a-5388-42d1-a1eb-3ebdf8b05706",
      "lifecycle_note": "未配置生命周期规则",
      "name": "probe-obj-1789645199",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "standard",
      "updated_at": "2026-09-18T07:39:24Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-17T11:39:38Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "cada9bcc-59a5-4fb9-aef1-ae622dcc1f10",
      "lifecycle_note": "未配置生命周期规则",
      "name": "probe-obj-1789645178",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "infrequent_access",
      "updated_at": "2026-09-17T11:39:38Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "public_read",
      "acl": "tenant_read",
      "acl_label": "租户内读",
      "created_at": "2026-09-17T10:52:17Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "67a319d3-7fd0-4b28-8080-e69ac66312fc",
      "lifecycle_note": "未配置生命周期规则",
      "name": "test0917",
      "object_count": 1,
      "region": "cn-east-1",
      "size_bytes": 28974,
      "storage_class": "standard",
      "updated_at": "2026-09-17T11:00:28Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "public_read",
      "acl": "tenant_read",
      "acl_label": "租户内读",
      "created_at": "2026-09-17T10:50:03Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "b2ade513-83ba-4c80-8c06-cd3937b49a5d",
      "lifecycle_note": "未配置生命周期规则",
      "name": "probe-obj-1789642202",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "infrequent_access",
      "updated_at": "2026-09-17T10:59:27Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-17T10:48:14Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "e122d69d-ae68-4c86-9ced-fedf72479ca9",
      "lifecycle_note": "未配置生命周期规则",
      "name": "probe-obj-1789642094",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "infrequent_access",
      "updated_at": "2026-09-17T10:48:14Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-17T10:47:27Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "5b2fdad1-7ac3-454a-ba64-62cf29ca1c3a",
      "lifecycle_note": "未配置生命周期规则",
      "name": "probe-obj-1789642047",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "infrequent_access",
      "updated_at": "2026-09-17T10:47:27Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "public_read",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-17T10:00:49Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "9317099d-68f4-4722-a0a1-e06024ebe32f",
      "lifecycle_note": "未配置生命周期规则",
      "name": "probe-bug39249",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "standard",
      "updated_at": "2026-09-17T10:00:49Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-14T07:41:12Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "5cc74ec8-77a6-46fd-9216-603dd08c6acf",
      "lifecycle_note": "未配置生命周期规则",
      "name": "123",
      "object_count": 1,
      "region": "cn-east-1",
      "size_bytes": 5,
      "storage_class": "standard",
      "updated_at": "2026-09-14T07:41:12Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-14T07:39:58Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "e7507bb7-2fe1-48c0-9686-62ef1b2222fe",
      "lifecycle_note": "未配置生命周期规则",
      "name": "test-ly-bucket2",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "standard",
      "updated_at": "2026-09-14T07:39:58Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-14T07:39:49Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "68d633ee-ecd2-43d8-b289-e70322de2d13",
      "lifecycle_note": "未配置生命周期规则",
      "name": "test-ly-bucket1",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "standard",
      "updated_at": "2026-09-14T07:39:49Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-14T07:39:28Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "42e2bcf9-846b-4622-b7fa-d5c0e046aff2",
      "lifecycle_note": "未配置生命周期规则",
      "name": "test-ly-bucket",
      "object_count": 1,
      "region": "cn-east-1",
      "size_bytes": 11266,
      "storage_class": "standard",
      "updated_at": "2026-09-14T07:39:28Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-10T11:44:06Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "dda98d0b-9a8b-42da-a32f-157fb7e30e11",
      "lifecycle_note": "未配置生命周期规则",
      "name": "tc-bucket-20260910-full2",
      "object_count": 2,
      "region": "cn-east-1",
      "size_bytes": 48,
      "storage_class": "standard",
      "updated_at": "2026-09-10T11:44:06Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-09-10T04:07:26Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "24ef01bb-5878-4e2f-b7c9-5f1e57d0fbf6",
      "lifecycle_note": "未配置生命周期规则",
      "name": "tc-bucket-20260910-obj1",
      "object_count": 0,
      "region": "cn-east-1",
      "size_bytes": 0,
      "storage_class": "standard",
      "updated_at": "2026-09-10T04:07:26Z",
      "versioning": "disabled"
    },
    {
      "access_mode": "private",
      "acl": "private",
      "acl_label": "私有",
      "created_at": "2026-08-31T03:17:56Z",
      "endpoint": "https://s3.cn-east-1.ani.local",
      "id": "c5095b66-295e-435e-b047-a7378b888d96",
      "lifecycle_note": "未配置生命周期规则",
      "name": "kb-docs",
      "object_count": 204,
      "region": "cn-east-1",
      "size_bytes": 1830193,
      "storage_class": "standard",
      "updated_at": "2026-08-31T03:17:56Z",
      "versioning": "disabled"
    }
  ],
  "next_cursor": null,
  "total": 14
}
[PASS] 6.list_http_200 http=200
[PASS] 6.deleted_absent count=14
```

### 6.7 重复删除 → 404

```
=== 7 DELETE /api/v1/buckets/{id}（重复删除） -> HTTP 404
{
  "code": "NOT_FOUND",
  "message": "capability resource not found: bucket 4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8 not found",
  "request_id": "req_22ac72bd-a89c-4cf2-abab-7f19d891a1f8"
}
[PASS] 7.repeat_http_404 http=404
```

### 6.8 桶级子操作 → 404

```
=== 8a GET /api/v1/buckets/{id}/objects（已删桶） -> HTTP 404
{
  "code": "NOT_FOUND",
  "message": "capability resource not found: bucket 4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8 not found",
  "request_id": "req_8dcbba47-00a1-4bbc-a52f-80a99fe94646"
}
[PASS] 8a.child_list_http_404 http=404
=== 8b PUT /api/v1/buckets/{id}/acl（已删桶） -> HTTP 404
{
  "code": "NOT_FOUND",
  "message": "capability resource not found: bucket 4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8 not found",
  "request_id": "req_81f355ff-7bad-42f6-ae15-d95c07709872"
}
[PASS] 8b.child_acl_http_404 http=404
```

### 6.9 同名重建 → 201、新 id

```
=== 9 POST /api/v1/buckets（同名重建） -> HTTP 201
{
  "access_mode": "private",
  "acl": "private",
  "acl_label": "私有",
  "created_at": "2026-09-18T09:11:42Z",
  "endpoint": "https://s3.cn-east-1.ani.local",
  "id": "0033b34a-11e0-4264-931c-2590578a2246",
  "lifecycle_note": "未配置生命周期规则",
  "name": "delprobe-1789722701",
  "object_count": 0,
  "region": "cn-east-1",
  "size_bytes": 0,
  "storage_class": "standard",
  "updated_at": "2026-09-18T09:11:42Z",
  "versioning": "disabled"
}
[PASS] 9.recreate_http_201 http=201
[PASS] 9.new_id_differs old='4ca6e8ee-b0b5-4c8d-bec6-568e1de299a8' new='0033b34a-11e0-4264-931c-2590578a2246'
```

### 6.10 清理

```
=== 11 清理 DELETE /api/v1/buckets/{id}（重建桶） -> HTTP 200
{
  "access_mode": "private",
  "acl": "private",
  "acl_label": "私有",
  "created_at": "2026-09-18T09:11:42Z",
  "endpoint": "https://s3.cn-east-1.ani.local",
  "id": "0033b34a-11e0-4264-931c-2590578a2246",
  "lifecycle_note": "未配置生命周期规则",
  "name": "delprobe-1789722701",
  "object_count": 0,
  "region": "cn-east-1",
  "size_bytes": 0,
  "storage_class": "standard",
  "updated_at": "2026-09-18T09:11:42Z",
  "versioning": "disabled"
}
[PASS] 11.cleanup_http_200 http=200
============================================================
FAIL=none
SKIP=['cross-tenant（tenant-b 登录失败）']
RESULT=PASS
```

### 6.11 跨主体边界补充实测（2026-09-18 09:20，同镜像）

用平台运营账号（`principal_kind=user` / `scope=platform`）尝试删除租户桶：

```
=== P0 platform login -> HTTP 200
{
  "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCIsImtpZCI6IiJ9.eyJzdWIiOiI3OGI5ODA1My05OWM3LTQ3OTYtODZmMS1kN2NiZTdkZDQ0YjIiLCJpc3MiOiJhbmktYXV0aC1zZXJ2aWNlIiwiZXhwIjoxNzg5NzI2ODUwLCJuYmYiOjE3ODk3MjMyNTAsImlhdCI6MTc4OTcyMzI1MCwianRpIjoiYzUxMzQ4MDEtODljYi00Y2Y1LTlhNjMtNWMyZDVjM2RkZDRkIiwicHJpbmNpcGFsX2tpbmQiOiJ1c2VyIiwidGlkIjoiIiwidWlkIjoiNzhiOTgwNTMtOTljNy00Nzk2LTg2ZjEtZDdjYmU3ZGQ0NGIyIiwiY3JlZGVudGlhbF9kb21haW4iOiJwbGF0Zm9ybSIsInNjb3BlIjoicGxhdGZvcm0iLCJyb2xlcyI6WyJwbGF0Zm9ybS1hZG1pbiJdfQ.qZN3tT0LnSNvtAgXrkQqiGmFjQfcOOVigJN4bhz8Fnb8nvO-rhTacHObZ-e9T0yEsxGCF6XRTYgENryD9MS8hMQi5sCBN83LxESBQry_HJDjI51tVN6pgoh4rTvV7WqoehSDHQl__1aD0ihAD10gV9TEJ6-6Q7_bpIW2NR1sAT_wtchdvGDqJPmZsetavyOW2U6fnXUM6n_A4yYQdwLmYd7zcwfAqPJYIYJ9_eCYmM3Jk6-E5jw1WxDUIx6mj0qf63jqQ316QH5mA08nyhUI8h8GfRAOL6s0KrLBUkc_1-4ISosNlDo0xgqfrB30VD91tmFOOF55hnhvDpkOorJe6g",
  "expires_in": 3600,
  "issued_at": "2026-09-18T09:20:50Z",
  "refresh_token": "ani_refresh_poPN0AGh0pSh1_UVRtVR5uDF8VVH-FrfzPbB-Rr9Ocg"
}
[PASS] P0.platform_login_200 http=200
=== P1 POST /api/v1/buckets（tenant-a 建探针桶） -> HTTP 201
{
  "access_mode": "private",
  "acl": "private",
  "acl_label": "私有",
  "created_at": "2026-09-18T09:21:22Z",
  "endpoint": "https://s3.cn-east-1.ani.local",
  "id": "599fd0ca-4954-4ecb-af7b-d8e004a8a275",
  "lifecycle_note": "未配置生命周期规则",
  "name": "delprobe-plat-1789723281",
  "object_count": 0,
  "region": "cn-east-1",
  "size_bytes": 0,
  "storage_class": "standard",
  "updated_at": "2026-09-18T09:21:22Z",
  "versioning": "disabled"
}
[PASS] P1.create_http_201 http=201
=== P2 DELETE /api/v1/buckets/{id}（平台 token） -> HTTP 403
{
  "code": "FORBIDDEN",
  "message": "token scope not allowed for this path",
  "request_id": "req_5731c2cb-ae81-46d4-88cf-e14f059496cb"
}
[PASS] P2.platform_principal_denied http=403
[PASS] P3.owner_record_intact count=1
=== P4 清理 DELETE /api/v1/buckets/{id}（tenant-a） -> HTTP 200
{
  "access_mode": "private",
  "acl": "private",
  "acl_label": "私有",
  "created_at": "2026-09-18T09:21:22Z",
  "endpoint": "https://s3.cn-east-1.ani.local",
  "id": "599fd0ca-4954-4ecb-af7b-d8e004a8a275",
  "lifecycle_note": "未配置生命周期规则",
  "name": "delprobe-plat-1789723281",
  "object_count": 0,
  "region": "cn-east-1",
  "size_bytes": 0,
  "storage_class": "standard",
  "updated_at": "2026-09-18T09:21:22Z",
  "versioning": "disabled"
}
[PASS] P4.cleanup_http_200 http=200
FAIL=none
RESULT=PASS
```

## 7. 未实测项与边界

### 7.1 环境曾被他批次覆盖，已于 09:34 重新覆盖部署并复测通过

时间线（均为 2026-09-18）：

| 时间 | 事件 |
|---|---|
| 09:11~09:21 | 本批次部署 `dev-20260918-bucketdel` 并完成 §6 的 24 项断言 |
| 09:23 前 | ani-system 被另一并发批次替换为 `fix-20260918-clusterdel`（该批次同时部署到 ani-test2，`test2-20260918-clusterdel`，与本批次无关）；此时探测 `DELETE /api/v1/buckets/{unknown}` 返回框架级 `404 page not found`（无 JSON body、无 `request_id`），证明未注册 |
| 09:34 | 经用户确认后**重新覆盖部署** `dev-20260918-bucketdel` 回 ani-system（[§7.1.1](#711-重新覆盖部署与复测)） |

即：**当前 ani-system 已包含本批次改动**，§6 与 §7.1.1 的证据均可复现。

#### 7.1.1 重新覆盖部署与复测

etcd 空间预检（两次读取，一致）：

```
"revision":73801707
"dbSize":912990208
"dbSizeInUse":145809408
"dbSizeQuota":2147483648
```

（912990208 / 2147483648 ≈ 42.5%，安全。）

部署变更（仅 `kubectl set image`，**未改任何 env**；部署后回读 env key 列表与部署前完全一致）：

```
IMAGE BEFORE: docker.changqingyun.cn/ani/ani-gateway:fix-20260918-clusterdel
deployment.apps/ani-gateway image updated
deployment "ani-gateway" successfully rolled out
IMAGE AFTER:  docker.changqingyun.cn/ani/ani-gateway:dev-20260918-bucketdel

NAME                          READY   STATUS    RESTARTS   AGE
ani-gateway-79b98585c-nrn9k   1/1     Running   0          14s
```

就绪探测（本环境 `/api/v1/healthz` 返回 404，不是健康检查路径，改用端点自身判定）：

```
DELETE /api/v1/buckets/00000000-...-000000000000 + 无效 token -> 产品级 JSON（非 "404 page not found"）
```

复跑 §8.1 矩阵结果：**24 项断言全 PASS / 0 FAIL**，1 项 SKIP（跨租户，原因同 §7.2）。

```
[PASS] 1.create_http_201 http=201
[PASS] 1.echo_id id='edb97402-088e-43ca-80f7-e55d94c774c8'
[PASS] 1.echo_name name='delprobe-1789724215'
[PASS] 2a.presign_http_200 http=200
[PASS] 2a.object_id_present object_id='77c7a271-7be4-4054-86c3-d59d9cddf1c6'
[PASS] 2b.presigned_put http=200
[PASS] 2c.list_objects_http_200 http=200
[PASS] 2c.object_id_resolved object_id='77c7a271-7be4-4054-86c3-d59d9cddf1c6'
[PASS] 3.nonempty_http_409 http=409
[PASS] 3.nonempty_code_conflict code='CONFLICT'
[PASS] 3.bucket_still_listed count=1
[PASS] 3.bucket_record_unchanged name='delprobe-1789724215'
[PASS] 4.delete_object_http_200 http=200
[PASS] 5.empty_http_200 http=200
[PASS] 5.echo_id id='edb97402-088e-43ca-80f7-e55d94c774c8'
[PASS] 5.echo_name name='delprobe-1789724215'
[PASS] 6.list_http_200 http=200
[PASS] 6.deleted_absent count=14
[PASS] 7.repeat_http_404 http=404
[PASS] 8a.child_list_http_404 http=404
[PASS] 8b.child_acl_http_404 http=404
[PASS] 9.recreate_http_201 http=201
[PASS] 9.new_id_differs old='edb97402-088e-43ca-80f7-e55d94c774c8' new='d73e5b7c-082e-4766-840a-b2400218d8c7'
[PASS] 11.cleanup_http_200 http=200
FAIL=none
SKIP=['cross-tenant（tenant-b 登录失败）']
RESULT=PASS
```

关键原始响应（不截断，本轮新证据）：

```
=== 3 DELETE /api/v1/buckets/{id}（桶内有对象） -> HTTP 409
{
  "code": "CONFLICT",
  "message": "capability resource conflict: bucket edb97402-088e-43ca-80f7-e55d94c774c8 still holds 1 object(s), delete them first",
  "request_id": "req_22c46269-4151-422a-ac0a-eb44d057b97a"
}
=== 7 DELETE /api/v1/buckets/{id}（重复删除） -> HTTP 404
{
  "code": "NOT_FOUND",
  "message": "capability resource not found: bucket edb97402-088e-43ca-80f7-e55d94c774c8 not found",
  "request_id": "req_64a1b8f6-b9a5-4994-99e8-53f1fe93bd06"
}
```

> 完整原始输出（含全部响应体与 request_id）保存在本机 `repo/.tmp/bucketdel_out.txt`（`.tmp` 已 gitignore，不入库）；结构与本记录 §6 各小节一一对应。

**副作用登记**：本次覆盖使另一个并发批次 `fix-20260918-clusterdel` 的改动在 ani-system 上**不再生效**（该批次在 ani-test2 上的 `test2-20260918-clusterdel` 未被触碰）。该镜像仍保留在构建机（image ID `aa9789cbf04b`），需要时可原样 `kubectl set image` 回滚；其来源分支不在本地工作区，本批次无法做并集构建，因此两批次在 ani-system 上**不可共存**。

### 7.2 未实测项

1. **跨租户删除未在测试环境实测**：tenant-b 登录返回 404 `TENANT_NOT_FOUND`，另试 60 个候选账号 × 4 组常见密码全部登录失败（无第二租户可用凭据）。改以平台主体做补充边界实测（§6.11，403 且租户记录不变）；**跨租户 404 由适配层单测覆盖**。
2. `GET /buckets/{bucket_id}/lifecycle-rules`、存储类型变更等其余桶级子操作在已删桶上的 404 未逐条实测（同一 `resolveBucket` 墓碑过滤路径，已覆盖 8a/8b 两例）。
3. live gate 不带 `--cleanup` 时仍不删桶（清理仅在 `--cleanup` 分支执行）。
4. ani-test2 未部署本批次。

## 8. 遗留项

1. **物理 MinIO 桶未回收**：软删后物理桶保留（可能为空），需运维或后续批次提供「物理桶不再被任何活跃租户桶使用且为空」的独占判定 + 回收能力。
2. **生命周期规则行不清理**：桶软删后 `resolveBucket` 已 404、新建同名桶是不同 `bucket_id`，规则不可达（无功能影响）。
3. **测试环境残留桶**：`kjs-study/修复bug/对象存储后端Bug详细分析.md` 记录的 3 个探针桶 + live gate 历史残留桶，可在本端点上线后逐个清理（需先确认桶内对象已清空）。
4. **桶详情接口未暴露**：`GET /buckets/{bucket_id}` 在 Ports 已有实现但无路由/契约。
5. **前端删除入口**（D6）：删除按钮停留位置（列表页/详情页）与二次确认交互需 Console 侧确认。
6. **对象墓碑历史数据**：修复前已删除的对象若未落盘墓碑，网关重启后仍会被当作活跃对象，导致对应桶无法通过非空判定删除（需逐桶重新删除对象或运维清理）。

## 9. 备注

- 幂等键：DELETE 不需要 `idempotency_key`（遵循 CLAUDE.md 第 4.5 条，仅 POST 创建与有副作用的 PUT/PATCH 必须带），与既有三个 DELETE 一致。
- 错误映射：沿用 `writeStorageError`，`ErrNotFound`→404、`ErrConflict`→409、`ErrInvalid`→400。
- 实测踩坑（脚本侧，非产品缺陷）：`StorageBucketRecord` 响应不含 `state`/`deleted_at` 字段，删除态只能通过列表可见性与子操作 404 断言；对已删桶执行 `PUT /acl` 仍要求 `idempotency_key`（400 先于 404 判定）。