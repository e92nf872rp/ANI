# Control Plane State Recovery PostgreSQL 设计

> 日期：2026-08-03
> 批次：`CONTROL-PLANE-STATE-RECOVERY-A`
> 范围：ANI Core 网络、存储、向量存储控制面
> 状态：待评审

## 目标

按照 7.29 Console 原型和现有 Core OpenAPI v1 契约，将网络、存储、向量存储从 Gateway 进程内 map 改为 PostgreSQL 权威控制面状态。真实 Provider 模式下，Gateway 重启或多副本切换后，同一租户必须继续通过既有 `GET/LIST` 查询到一致资源。HTTP 重复提交继续由现有 Redis 幂等中间件处理。

本设计不修改 `repo/api/openapi/v1.yaml`，不新增 ANI Services 资源，也不在本批补齐 Provider delete、扩容、挂载、LB 后端组等独立生命周期能力。

## 已确认原则

1. 采用领域关系表，不使用通用 JSONB 资源表或事件溯源重构。
2. PostgreSQL 是控制面身份、期望配置、状态、关系和删除墓碑的权威来源。
3. Kube-OVN、Kubernetes/Ceph、MinIO、Milvus 是对应物理资源或数据内容的权威来源。
4. `GET/LIST` 按请求直接读取 PG，不在 Gateway 启动时把全量资源加载回内存。
5. Provider 观测和 reconcile 只负责把物理状态更新回 PG。
6. 真实 Provider 模式必须配置可用的 `DATABASE_URL`；连接失败或必需 schema 缺失时 Gateway 启动失败，不允许降级为内存模式。
7. 显式 `local` profile 继续允许内存实现，用于单元测试和本地契约开发。
8. 删除使用软删除。API 默认过滤 `deleted_at IS NOT NULL` 或 `state=deleted`，对外保持现有 404 语义。
9. 资源墓碑暂不自动物理清理。
10. Redis 继续负责 API 写请求的 24 小时去重、请求指纹冲突和响应重放；PG 不再重复建立通用幂等响应表。
11. 内部 Service 调用不经过 HTTP middleware；每个可创建资源表保存自己的 `create_idempotency_key` 和 `create_request_fingerprint`，沿用实例域的资源内幂等模式。

## 数据来源边界

| 数据 | 权威来源 | PG 保存内容 |
|---|---|---|
| VPC、子网、安全组、路由、LB | PostgreSQL | 身份、期望配置、状态、关系、Provider refs |
| IP 分配 | PostgreSQL 控制面摘要 | IP、占用方、状态、最近观测时间 |
| 卷、快照、文件系统、挂载关系 | PostgreSQL | 身份、配置、状态、挂载关系、历史、Provider refs |
| 对象桶设置 | PostgreSQL | 桶身份、ACL、默认存储类型、生命周期规则、统计摘要 |
| 桶内对象内容和目录浏览 | MinIO | PG 只保存通过 Core 创建的对象元数据和写操作结果 |
| 向量库定义、索引摘要、KB 关联 | PostgreSQL | collection 身份、维度、度量、状态、计数摘要、关联 |
| 向量文档和检索结果 | Milvus | PG 不保存 embedding 和搜索结果；异步任务继续写 `async_tasks` |
| 24 小时 HTTP 幂等状态 | Redis | 请求指纹、处理中锁、状态码和响应快照 |

## 通用字段与约束

所有租户资源表统一遵守：

- `tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE`
- 业务资源 ID 沿用当前字符串 ID；v1 明确要求 UUID 的 bucket 使用 UUID。
- 主键或唯一键必须包含 `tenant_id`，跨表引用使用 `(tenant_id, resource_id)` 复合外键。
- 主资源保存 `state`、`reason`、`created_at`、`updated_at`、`deleted_at`。
- 可创建资源保存 `create_idempotency_key TEXT` 和 `create_request_fingerprint TEXT`；同一资源类型内建立 `(tenant_id, create_idempotency_key)` 唯一约束。
- 真实 Provider 资源保存 `provider`、`provider_refs JSONB`、`last_observed_at`。
- `provider_refs` 只保存非敏感资源定位信息，不保存 Token、密码、预签名 URL 或完整凭据。
- 所有用户可筛选字段建立 tenant-first 索引；默认列表索引包含 `updated_at DESC`。
- 名称是否唯一只按现有 v1 行为执行。本批不新增未声明的全局名称唯一语义。

## 一、幂等与中断恢复边界

### HTTP 重复提交

沿用现有 Gateway Redis 幂等中间件：

- key scope：租户、HTTP method、path 和客户端 idempotency key。
- `SETNX` 防止同一请求并发执行。
- 请求指纹不同返回 `IDEMPOTENCY_KEY_REUSED`。
- 成功或失败响应保存 24 小时并在 Gateway 重启后重放。
- Redis 不可用时返回 `503 IDEMPOTENCY_UNAVAILABLE`，不得绕过幂等保护执行 Provider 写操作。

PG 不保存 HTTP 响应快照、预签名 URL 或通用幂等记录。

### 内部 Service 创建幂等

Network、Storage 和 Vector Store ports 会被 Gateway handler、实例编排和测试直接调用，不能把正确性完全建立在 HTTP middleware 上。对所有带创建语义的资源和子资源：

1. 在资源自身表保存 `create_idempotency_key` 和规范化请求的 `create_request_fingerprint`。
2. 同一 `(tenant_id, resource_type, create_idempotency_key)` 只对应一个资源；实际通过各资源表自己的 tenant-scoped unique 实现，不建立通用跨域表。
3. 同 key、同 fingerprint：读取并返回原资源。
4. 同 key、不同 fingerprint：返回 `ports.ErrConflict`，由 handler 映射现有 v1 冲突语义。
5. 旧数据没有历史 key 时保持 NULL，不伪造幂等事实。

这与现有实例域在 `workload_instances` / `workload_instance_operations` 内持久化幂等键的模式一致。v1 对外保证至少 24 小时重放；Redis 负责 24 小时 HTTP 响应窗口，PG 资源键同时防止内部直调和多副本创建重复资源。

### 资源自然幂等

- 主资源 ID 在 Provider 调用前生成并写入 PG `pending` 行。
- Provider 对象名称由 tenant/resource ID 确定，不使用进程内序号。
- 创建类内部调用先按本资源表的 `create_idempotency_key` 原子查重，再决定是否生成资源。
- 安全组绑定、IP 分配、监听器、挂载和 KB 关联通过活跃关系唯一约束防止重复关系。
- expand、ACL、storage class、policy 等设置型操作以目标状态为结果，重复 apply 不产生第二个逻辑资源。
- Gateway 在 Provider 调用中断后，由 reconcile 扫描 PG `pending` 资源并按相同资源 ID observe/apply。

本批保证“已完成请求在 Gateway 重启后可查询”和“重复提交不产生重复资源”。Redis 中仍为 `processing` 时的主动响应恢复属于 Gateway 幂等中间件后续优化，不通过新增 PG 通用表解决。

## 二、网络表

### 2.1 主资源

#### `network_vpcs`

保留现有字段，增加：

- `provider TEXT`
- `provider_refs JSONB NOT NULL DEFAULT '[]'`
- `last_observed_at TIMESTAMPTZ`
- `deleted_at TIMESTAMPTZ`

索引：`(tenant_id, state, updated_at DESC)`、`(tenant_id, cidr) WHERE deleted_at IS NULL`。

#### `network_subnets`

保留现有 VPC 复合外键，增加 Provider 和软删除字段。索引覆盖：

- `(tenant_id, vpc_id, state, updated_at DESC)`
- `(tenant_id, cidr) WHERE deleted_at IS NULL`

CIDR 位于 VPC 内和重叠校验属于下一批业务约束，但本表结构支持在同一事务内查询校验。

#### `network_security_groups`

保留主资源字段。现有 `rules JSONB` 在迁移期保留为兼容列，但新 Store 不再把它作为权威来源；规则改由子表聚合。

#### `network_load_balancers`

保留 `vpc_id`、`subnet_id`、`scheme`、`vip`，增加复合外键、Provider 和软删除字段。现有 `listeners JSONB` 在迁移期保留，权威监听器改由子表聚合。

#### `network_routes`

保留现有字段，补齐内部 `state`、`reason`、Provider、观测和软删除字段。修复 RLS 使用的 session key，从错误的 `ani.tenant_id` 统一为 `app.current_tenant_id`。

### 2.2 网络子资源

#### `network_security_group_rules`

- `tenant_id UUID`
- `rule_id TEXT`
- `security_group_id TEXT`
- `priority INTEGER CHECK 1..32766`
- `direction TEXT CHECK ingress/egress`
- `protocol TEXT CHECK tcp/udp/icmp/all`
- `port_range TEXT`
- `cidr TEXT`
- `action TEXT CHECK allow/deny`
- `description TEXT`
- `created_at`、`updated_at`、`deleted_at`

主键 `(tenant_id, rule_id)`；复合外键指向安全组。活跃规则唯一约束至少覆盖 `(tenant_id, security_group_id, priority, direction, rule_id)`，不擅自禁止 v1 允许的相同优先级规则。

#### `network_security_group_bindings`

- `binding_id TEXT`
- `security_group_id TEXT`
- `target_type TEXT CHECK instance/network_interface/load_balancer`
- `target_id TEXT`
- `created_at`、`deleted_at`

活跃绑定唯一：`(tenant_id, security_group_id, target_type, target_id) WHERE deleted_at IS NULL`。由于目标是多态资源，PG 无法使用单一外键；Service 在事务内验证目标存在，删除拦截直接查询本表。

#### `network_subnet_ip_allocations`

- `allocation_id TEXT`
- `subnet_id TEXT`
- `ip_address INET`
- `resource_type TEXT`
- `resource_id TEXT`
- `state TEXT CHECK available/allocated/reserved`
- `created_at`、`updated_at`、`last_observed_at`、`deleted_at`

活跃 IP 唯一：`(tenant_id, subnet_id, ip_address) WHERE deleted_at IS NULL`。

#### `network_load_balancer_listeners`

- `listener_id TEXT`
- `load_balancer_id TEXT`
- `protocol TEXT CHECK http/https/tcp`
- `port INTEGER CHECK 1..65535`
- `target_port INTEGER CHECK 1..65535`
- `created_at`、`updated_at`、`deleted_at`

活跃监听端口唯一：`(tenant_id, load_balancer_id, protocol, port) WHERE deleted_at IS NULL`。

原型中的后端组、权重、健康检查、监控和事件尚无 v1 契约，本批不提前建产品表。

## 三、存储表

### 3.1 块存储

#### `storage_volumes`

在现有表增加 v1 已声明字段：

- `zone`、`volume_type`、`iops`、`encrypted`
- `mount_instance_id`、`mount_route`、`mount_name`
- `os_init_status`、`os_init_device`
- `from_snapshot_id`、`from_snapshot_name`
- Provider、观测和软删除字段

`snapshots_count` 由快照表聚合，不保存为独立权威计数。

#### `storage_volume_auto_snapshot_policies`

以 `(tenant_id, volume_id)` 为主键的一对一表，保存 `enabled`、`retain_days`、`schedule`、`updated_at`。删除卷时保留随主资源墓碑可追溯，默认查询过滤已删除卷。

#### `storage_volume_mount_events`

保存 v1 `mount_history`：`event_id`、`volume_id`、`action`、`target`、`result`、`occurred_at`。只追加，不原地覆盖历史。

#### `storage_volume_snapshots`

保存 `snapshot_id`、`volume_id`、`name`、`description`、`status`、`size_bytes`、Provider refs、`created_at`、`updated_at`、`deleted_at`。

### 3.2 文件存储

#### `storage_filesystems`

在现有表增加 `zone`、`performance_mode`、`mount_command`、Provider、观测和软删除字段。`mounts` 由活跃 attachment 数量聚合。

#### `storage_filesystem_mount_targets`

保存 `mount_target_id`、`filesystem_id`、`subnet_id`、`vpc_id`、`ip_address INET`、`status`、Provider refs 和时间字段。通过 `(tenant_id, filesystem_id)`、`(tenant_id, subnet_id)` 复合外键建立跨域关系。

#### `storage_filesystem_attachments`

保存 `attachment_id`、`filesystem_id`、`instance_id`、`instance_name`、`instance_route`、`mount_path`、`ip_address`、`protocol`、`auto_mount`、`attached_at`、`detached_at`。

活跃绑定唯一：`(tenant_id, filesystem_id, instance_id, mount_path) WHERE detached_at IS NULL`。实例外键使用现有实例表真实主键形态；若现有实例表无法提供复合外键，则保持 tenant-scoped 应用校验，不放宽 RLS。

### 3.3 对象存储

#### `storage_buckets`

保存 v1 已声明字段：`bucket_id UUID`、`name`、`region`、`endpoint`、`access_mode`、`acl`、`acl_label`、`storage_class`、`versioning`、统计摘要、Provider、观测和时间字段。

活跃桶名唯一：`(tenant_id, name) WHERE deleted_at IS NULL`。`endpoint` 不包含凭据。

#### `storage_bucket_lifecycle_rules`

保存 `rule_id`、`bucket_id`、`name`、`prefix`、`expire_days`、`to_infrequent_days`、`enabled` 和时间字段。替换规则必须在同一 tenant transaction 中完成。

#### `storage_objects`

扩展现有表：增加 `bucket_id UUID`、`storage_class`、Provider refs 和软删除字段，同时保留 `bucket` 名称列兼容旧记录。通过 Core 上传产生的对象写元数据；MinIO 对象浏览仍按请求读取 Provider，避免把对象内容目录复制成第二套权威数据。

前缀是对象存储命名语义，不单独建立目录表。创建前缀产生的零字节对象按普通对象元数据记录。

## 四、向量存储表

### `vector_stores`

- `tenant_id UUID`
- `vector_store_id TEXT`
- `name TEXT`
- `dimension INTEGER CHECK > 0`
- `metric TEXT CHECK cosine/l2/ip`
- `embedding_model TEXT`
- `vector_count BIGINT CHECK >= 0`
- `index_status TEXT CHECK building/ready/failed`
- `last_indexed_at TIMESTAMPTZ`
- `state TEXT CHECK pending/ready/failed/deleting/deleted`
- `reason TEXT`
- `provider TEXT`
- `provider_refs JSONB`
- `last_observed_at`、`created_at`、`updated_at`、`deleted_at`

主键 `(tenant_id, vector_store_id)`；列表索引 `(tenant_id, state, updated_at DESC)`。

### `vector_store_knowledge_base_links`

保存 `vector_store_id`、`knowledge_base_id`、`knowledge_base_name`、`source`、`created_at`、`updated_at`、`deleted_at`。现有 v1 返回单个 `knowledge_base_ref`，因此每个向量库只允许一个活跃关联。

向量文档、embedding 和检索结果不进入 PG。文档插入任务继续使用现有 `async_tasks`，任务的 `resource_id` 指向 `vector_store_id`。

## 五、RLS 和权限

所有新增表：

1. `ENABLE ROW LEVEL SECURITY` 和 `FORCE ROW LEVEL SECURITY`。
2. policy 同时定义：

```sql
USING (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
WITH CHECK (tenant_id = NULLIF(current_setting('app.current_tenant_id', true), '')::uuid)
```

3. `ani_app` 只获得必需的 `SELECT/INSERT/UPDATE/DELETE`。
4. 跨租户资源继续对外表现为 404，不泄露存在性。
5. platform migration/maintenance 使用平台连接执行，不在普通租户请求中关闭 RLS。

## 六、写事务和中断恢复

### 创建流程

1. tenant transaction 按本资源表的创建幂等键和 fingerprint 原子查重。
2. 首次请求生成稳定资源 ID，插入带创建幂等信息的 `pending` 资源并提交。
3. 使用确定性 Provider 名称执行 apply。
4. 新 tenant transaction 更新 Provider refs 和最终状态。
5. Provider 失败写 `failed`，不得删除 PG 记录掩盖失败。

外部 Provider 调用不放在长时间 PG transaction 内。

### 中断窗口

- PG pending 已提交、Provider 尚未执行：reconcile 按相同资源 ID 继续执行。
- Provider 已成功、最终 PG 更新前 Gateway 中断：通过稳定 Provider 名称 observe，回写同一资源为 available/ready。
- PG 更新失败：API 不得返回成功；资源保持 pending/failed，供 reconcile 修复。
- 重复 HTTP 请求：由 Redis 中间件重放或拦截；内部 reconcile 始终复用 PG 资源 ID。
- 重复内部创建调用：由对应资源表的创建幂等键返回原资源，不依赖进程内 map。

### 查询流程

- 主资源 `GET/LIST` 每次读取 PG。
- 子资源从对应关系表聚合；不依赖 Service map。
- MinIO 桶对象浏览和 Milvus 搜索按请求访问 Provider，但先从 PG 校验租户资源身份。
- Provider 实时健康不应阻断普通元数据列表；状态由 reconcile 更新。

## 七、旧数据迁移

迁移必须 additive，不删除现有列或旧记录：

1. 创建新表并给现有主表增加 nullable/default 字段。
2. 将 `network_security_groups.rules` 展开回填到规则表；无法解析的行使迁移失败并报告资源 ID，不静默丢弃。
3. 将 `network_load_balancers.listeners` 展开到监听器表。
4. 旧资源的 `create_idempotency_key` 和 fingerprint 保持 NULL，不伪造历史请求。
5. 旧存储扩展字段按 v1 合理空值/default 回填；不伪造挂载历史、快照和 KB 关联。
6. 修正 `network_routes` RLS session key。
7. 在切换 Store 读取前运行 tenant、复合外键、重复活跃绑定和非法状态预检。
8. 生产验证使用隔离 tenant/resource 前缀，不清空非本批数据。

## 八、Gateway 装配

真实模式使用一次共享 `MetadataStore` 连接，注入：

- `MetadataNetworkStore`
- `MetadataStorageStore`
- `MetadataVectorStoreStore`

启动顺序：

1. 判断 Network/Storage/Vector 是否任一配置为真实 Provider。
2. 要求非空 `DATABASE_URL`。
3. 连接并 `Ping` PG。
4. 检查本批必需表和 schema marker。
5. 构造 Store 和 Service。
6. 任何一步失败均退出，不注册真实资源路由。

Local profile 不连接 PG，继续使用独立内存 Service。

## 九、验收标准

### 自动化测试

- Store 的 create/get/list/update/delete、RLS tenant 参数、cursor 和软删除过滤。
- 现有 Redis 中间件在 Gateway 重启后仍能重放同键请求，并拒绝同键不同指纹。
- 两个 Service 实例共享同一 PG 时，内部创建同 key/同 fingerprint 返回同一资源，同 key/不同 fingerprint 冲突。
- 子资源聚合：安全组规则/绑定、LB listener、卷历史/快照、文件系统挂载、桶规则、KB 关联。
- PG 写失败不得返回成功。
- 真实 Provider 模式缺少或连接不上 PG 时 runtime 构造失败。
- Local profile 不受影响。

### 真实环境闭环

同一隔离租户创建至少：

- VPC、Subnet、Security Group、Rule、Binding、Route、Load Balancer
- Volume、Snapshot、Filesystem、Mount Target、Bucket、Object metadata
- Vector Store、KB link、文档插入 task

记录重启前 `GET/LIST` 响应摘要和 PG 行；rollout restart Gateway 后：

1. 原资源 ID 仍返回 200。
2. 列表数量、关系和关键字段一致。
3. 通过现有 Redis 重放原幂等键，不新增 PG 行、Kube-OVN/Ceph/MinIO/Milvus 资源。
4. 同键不同请求返回冲突。
5. 跨租户查询返回 404 或空列表。
6. 删除后 API 不再返回资源，PG 墓碑仍存在。
7. evidence 不包含 Token、密码、内部完整端点或预签名 URL。

## 十、原型与 v1 契约之外

以下原型能力当前没有足够 v1 契约，本批不得静默实现或提前改变返回：

- VPC description、Subnet 可用区字段
- 安全组模板、复制、安全组作为规则源/目标
- LB 后端组、权重、健康检查、监控和事件
- 向量存储文本检索输入
- 原型要求但 v1 未声明的额外删除/更新行为

如确认进入 P0，应先提交独立 v1 契约 PR，再实施对应表和行为。
