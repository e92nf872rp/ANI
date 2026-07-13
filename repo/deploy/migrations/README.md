# ANI Platform migrations

按文件名时间戳顺序应用。生产/已有集群增量升级使用本目录。

## Gateway 元数据（P0–P3）

| 文件 | 内容 |
|------|------|
| `20260629_014_gateway_metadata_alignment.sql` | reconcile 索引、`network_routes` RLS 对齐 |
| `20260629_015_gateway_metadata_p1.sql` | `vector_stores`、`metering_token_reports`、`metering_records` |
| `20260629_016_gateway_metadata_p3.sql` | `storage_buckets`、`volume_snapshots`、`filesystem_mount_targets` |
| `20260629_017_gateway_metadata_p5.sql` | `registry_projects`、`registry_repository_permissions`、`registry_pull_secrets` |
| `20260713_002_sandbox_workload_kind.sql` | `instance_plan_audits` / `workload_instances` 接受 `workload_kind='sandbox'` |

**本地开发快捷方式**：014–017 已合并进 `deploy/postgres/gateway-metadata-schema.sql`（幂等）。新空卷 `make deps` 已内联进 `deploy/postgres/ani-dev-database-init.sql`，无需单独执行。

```bash
# 已有 PG 卷一次性补齐 Gateway 元数据
make db-upgrade-gateway-metadata
```

## 完整 dev 初始化

见 [`deploy/postgres/README.md`](../postgres/README.md)。
