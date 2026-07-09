# ANI PostgreSQL · 开发与 Gateway 元数据

## 文件说明

| 文件 | 用途 |
|------|------|
| `ani-dev-database-init.sql` | **新环境完整初始化**（auth + Core runtime + Gateway 元数据与实例网络字段）。符号链接 → `deploy/real-k8s-lab/auth-dex-production-db-init.sql` |
| `gateway-metadata-schema.sql` | **已有库增量升级**（幂等）。合并 Gateway 元数据表与实例网络字段对齐 |

`deploy/docker/init-scripts/postgres/001-ani-dev-database-init.sql` 在 `make deps` 空卷时自动执行上述完整脚本。

## 新开发环境（推荐）

```bash
# 空卷：一键拉起依赖并完成 PG 初始化
make deps-clean   # 可选：仅当需要重建库
make deps

# Gateway 启用 PG 元数据
export DATABASE_URL='postgres://ani:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable'
export ANI_AUTH_MODE=dev
export GATEWAY_REDIS_URL='redis://:ani_dev_password@127.0.0.1:6379/0'
```

无需再手动执行 Gateway 元数据和实例网络字段相关 migration。

## 已有 PostgreSQL 数据卷

`docker-entrypoint-initdb.d` 只在**首次建卷**时运行。旧卷请执行：

```bash
export DATABASE_URL='postgres://ani:ani_dev_password@127.0.0.1:5432/ani?sslmode=disable'
make db-upgrade-gateway-metadata
```

或：

```bash
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f deploy/postgres/gateway-metadata-schema.sql
```

## 生产 / 增量 migration 目录

`deploy/migrations/` 保留按时间戳的增量脚本。新装 dev 以 `ani-dev-database-init.sql` 为准；已有环境可逐条跑 migration 或使用 `gateway-metadata-schema.sql` 一次性补齐 Gateway 元数据表和实例网络字段。

## Gateway 元数据覆盖表

| 批次 | 表 |
|------|-----|
| P0 | `workload_*`、`network_*`、`storage_volumes/filesystems/objects`（在 init 前半段） |
| P1 | `vector_stores`、`metering_token_reports`、`metering_records` |
| P3 | `storage_buckets`、`volume_snapshots`、`filesystem_mount_targets` |
| P4 | `platform_branding` PUT（无新表） |

## 验收

```bash
cd repo
make db-upgrade-gateway-metadata   # 已有卷
make validate-gateway-metadata     # 逻辑 gate
```
