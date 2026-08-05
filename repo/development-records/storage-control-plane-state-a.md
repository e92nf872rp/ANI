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
