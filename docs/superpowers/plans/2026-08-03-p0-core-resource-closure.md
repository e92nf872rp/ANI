# ANI Core P0 四模块闭环实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 按 7.29 Console 最新原型和现有 Core OpenAPI v1，将镜像、存储、网络、实例拆成四个可独立评审、独立上线、独立 live 验收的 P0 模块，并在依赖闭合后完成实例端到端收口。

**架构：** 镜像内容继续以 Harbor 为权威；存储和网络控制面以 PostgreSQL 为权威；Ceph、MinIO、Milvus、Kube-OVN 保持物理资源或数据内容权威；实例继续复用现有 PostgreSQL `workload_instances` 和 operation 表，不重建实例主表。所有新增公开字段、枚举和路径先走纯 v1 契约 PR，批准后才进入实现。

**技术栈：** Go 1.25、PostgreSQL 17、pgx v5、Redis、Harbor/Trivy、Kube-OVN、Kubernetes/KubeVirt、Rook-Ceph、MinIO、Milvus、Python 3 live-gate validator。

## 全局约束

- 仅修改 ANI Core；不实现 ANI Services、Console 或 BOSS 页面。
- `repo/api/openapi/v1.yaml` 是唯一 Core 对外契约；契约 PR 与实现 PR 严格分开。
- 契约 PR 只允许兼容新增：可选 request/response 字段、新端点、新枚举值；不得删除或改变既有字段类型、HTTP 方法和错误语义。
- 涉及契约变更的模块必须先提交纯契约 PR，并通过个人仓库 CI 后再提交上游；契约未批准前不得写对应 handler、port、adapter 或迁移。
- 真实 Provider 模式必须 fail closed；所需 PostgreSQL、Redis 或 Provider 不可用时不得回退内存并伪造成功。
- 显式 local profile 可保留内存实现，但响应必须继续暴露 `dev_profile`。
- PostgreSQL 租户表使用 tenant-first key、复合外键、forced RLS 和 `app.current_tenant_id`。
- Redis 负责 HTTP 24 小时响应重放；资源表保存自身 create idempotency key/fingerprint，不新增通用 PG HTTP 幂等表。
- Provider 调用不得放在长 PostgreSQL 事务中；先提交 `pending` 意图，再 apply/observe，最后回写终态。
- Token、密码、Secret 明文、完整内部端点、预签名 URL、对象正文、embedding 不得写入 PG 或 evidence。
- 事件和操作历史属于横切能力；不得在镜像、存储、网络各自发明不兼容的事件表。本计划只复用现有实例 operation/task/audit 边界，通用资源事件契约另行评审。
- 每个模块分别更新 development record；不得在一个完成记录中笼统声明四个模块全部 production ready。
- 未经人工确认，不 commit、push、创建 PR、应用真实迁移、重启 Gateway 或执行真实写 gate。

## 模块依赖

```text
REGISTRY-P0-CLOSURE-A -----------+
                                  |
STORAGE-CONTROL-PLANE-STATE-A ----+--> INSTANCE-P0-CLOSURE-A
                                  |
NETWORK-P0-CONTRACT-A             |
  -> NETWORK-CONTROL-PLANE-STATE-A+
  -> NETWORK-PRIVATE-VPC-LIVE-A --+
```

- 镜像、存储、网络可以并行设计；存储、网络和实例的契约 PR 必须各自串行通过评审，镜像按现有 v1 实现，不新增契约。
- 实例主契约和 PG 主表已经存在；实例最终 live closure 依赖镜像、存储和网络完成。
- 旧计划 `2026-08-03-control-plane-state-recovery.md` 保留为数据库设计输入，不再作为单一大批次直接执行。

---

## 工作流 A：镜像 `REGISTRY-P0-CLOSURE-A`

### Task A1：冻结镜像 P0 现有契约范围

**文件：**
- 修改：`repo/scripts/validate_openapi_spec_test.py`

**冻结结论：**
- 复用现有 `/registry/images`、projects、repositories、artifacts、push-instructions、scan-report、scan-result、references 和 delete-tag 契约。
- 原型关键字搜索由 Console 把输入映射到现有 project/repository/tag 条件；本批不增加语义模糊的服务端 keyword。
- 最新 7.29 原型只要求漏洞扫描展示；现有 scan status 和 critical/high/medium/low 摘要足够本批使用，不新增手动重扫或 CVE 分页接口。
- 保留历史 repository permission API 以兼容旧客户端，但 P0 Console 不展示项目权限 Tab。

- [x] 增加契约回归断言，固定 purpose 四类枚举、scan summary、引用列表、删除 409 和 createInstance 镜像 422 原因码。
- [x] 运行 `make validate-openapi-spec`、`make validate-core-api-compatibility` 和 `git diff --check`，证明不需要镜像 v1 变更。
- [x] 未发现现有 schema 无法表达真实 Harbor 响应；后续如发现缺口，停止实现并重新进入独立契约评审，不在 handler 私加字段。

### Task A2：收紧 Harbor 镜像用途和扫描摘要实现

**文件：**
- 修改：`repo/pkg/ports/image_registry.go`
- 修改：`repo/pkg/adapters/registry/harbor_image_registry.go`
- 修改：`repo/pkg/adapters/registry/harbor_image_registry_test.go`
- 修改：`repo/pkg/adapters/registry/local_image_registry.go`
- 修改：`repo/pkg/adapters/registry/local_image_registry_test.go`
- 修改：`repo/services/ani-gateway/internal/router/registry_resources.go`
- 修改：`repo/services/ani-gateway/internal/router/registry_resources_test.go`

- [x] 先写失败测试：project/repository/tag/purpose/scan_status 组合过滤、跨租户不可见、Trivy 摘要不可用时显式失败。
- [x] 保持 `ports.ImageRegistry` typed request/result，不增加通用 `map[string]any` 返回。
- [x] Harbor adapter 把 artifact scan overview 映射到现有 scan status 和四级计数，禁止缺数据时伪造 complete/0。
- [x] 固定 purpose 优先读取 Provider 可持久 Label `ani-purpose-{container|gpu|sandbox|system}`；历史 artifact 才使用向后兼容的 repository 命名推导。
- [x] 跑 registry adapter、router、async task 聚焦测试。

### Task A3：完成实例创建镜像门禁

**文件：**
- 修改：`repo/pkg/adapters/runtime/instance_resource_resolver.go`
- 修改：`repo/pkg/adapters/runtime/instance_resource_resolver_test.go`

- [x] 写失败测试覆盖 `not_scanned/pending/running/complete/failed` 和 critical/high 计数。
- [x] 保留现有 `ImageNotFound`、`ImagePurposeMismatch`；实现 `ImageScanning` 和 `ImageVulnerabilityBlocked`。
- [x] 策略关闭时允许高危镜像但保留可审计的 resolver 结果；策略来源不得硬编码到 Console。
- [x] 固定镜像用途优先读取 Provider 元数据；仅对历史 artifact 使用 repository 命名兼容推导。
- [x] 跑 instance resolver 和 create handler 测试。

### Task A4：镜像真实闭环

**文件：**
- 修改：`repo/deploy/real-k8s-lab/registry-harbor-live-gate.yaml`
- 修改：`repo/scripts/validate_registry_harbor_live_gate.py`
- 修改：`repo/scripts/validate_registry_harbor_live_gate_test.py`
- 创建：`repo/development-records/registry-p0-closure-a.md`
- 创建（执行后）：`repo/development-records/live-evidence/registry-p0-closure-live-20260803.json`

- [x] 静态 gate 覆盖 project、push instructions、真实 push、扫描状态、漏洞读取、purpose 过滤、实例引用和删除 409。
- [x] 经人工确认后执行 push -> scan -> create instance -> reference -> blocked delete -> cleanup。
- [x] evidence 只保存资源 ID、状态、计数和响应哈希，不保存 Token、密码或 Registry 登录命令中的凭据。
- [x] 单独记录镜像模块 readiness，不声明 BOSS quota/GC 已完成。

---

## 工作流 B：存储 `STORAGE-CONTROL-PLANE-STATE-A`

### Task B1：冻结存储 P0 现有契约范围（不改 v1）

**文件：**
- 修改：`repo/scripts/validate_openapi_spec_test.py`

**契约边界：**
- 不修改 `repo/api/openapi/v1.yaml`；存储 P0 控制面状态收敛复用现有 volume/snapshot/filesystem/mount-target/object/vector/KB-link 契约。
- 不新增 `VectorStore.description` 或其他公开字段。
- 原型“文件系统权限”在本批只使用现有租户 RLS、挂载 attachment `read_only` 和安全组边界展示；未冻结 NFS client/CIDR ACL 语义前不新增字段。
- 文本转向量不加入 Core；`VectorStoreSearchRequest` 继续只接收 vector。
- SMB、跨区域对象复制、静态网站和完整 ACL 矩阵不进入 P0。

- [x] 增加契约回归断言，证明现有 v1 已覆盖 P0 存储资源面，且不引入 description/ACL/SMB/text-search 等新字段。
- [x] 运行 `make validate-openapi-spec`、`make validate-core-api-compatibility` 和 `git diff --check`，证明不需要存储 v1 变更。
- [x] 未发现必须改契约才能进入 B2；后续如发现缺口，停止实现并重新进入独立契约评审。

### Task B2：建立存储独立 PG migration 和 schema gate

**文件：**
- 创建：`repo/deploy/migrations/20260803_001_storage_control_plane_state.sql`
- 创建：`repo/scripts/validate_storage_control_plane_state.py`
- 创建：`repo/scripts/validate_storage_control_plane_state_test.py`
- 修改：`repo/Makefile`

- [x] 写 validator 失败用例，要求 volume、snapshot、mount history、filesystem、mount target、attachment、bucket、lifecycle rule、Core object metadata、vector store 和 KB link 表。
- [x] migration 使用 tenant-first key、复合外键、forced RLS、soft delete、state check 和资源内 create idempotency 唯一约束。
- [x] 旧 JSONB/旧列只做可验证的 additive backfill；格式异常时 migration 失败，不静默丢行。
- [x] 不保存对象正文、预签名 URL、embedding 或向量检索结果。
- [x] 运行 schema validator（`make validate-storage-control-plane-state`）和 `git diff --check`；真实库 apply 待人工批准后执行。
- [x] 真实 PG migration 已人工批准并 apply：`ani-system/ani-postgres-0` 上 `20260803_001_storage_control_plane_state.sql` COMMIT；brownfield 兼容已有 `storage_buckets`/`vector_stores`（`store_id`→`vector_store_id`，`idempotency_key` backfill）。

### Task B3：让 Storage Store 和 Service 以 PG 为权威

**文件：**
- 修改：`repo/pkg/ports/storage_resources.go`
- 修改：`repo/pkg/adapters/runtime/storage_store.go`
- 修改：`repo/pkg/adapters/runtime/storage_store_test.go`
- 修改：`repo/pkg/adapters/runtime/storage_service.go`
- 修改：`repo/pkg/adapters/runtime/storage_service_test.go`
- 修改：`repo/pkg/ports/vector_store.go`
- 创建：`repo/pkg/adapters/runtime/vector_store_store.go`
- 创建：`repo/pkg/adapters/runtime/vector_store_store_test.go`
- 修改：`repo/pkg/adapters/runtime/vector_store_service.go`
- 修改：`repo/pkg/adapters/runtime/vector_store_service_test.go`

- [x] 先写两个 Service 实例共享同一 Store 的失败测试，覆盖 create/get/list/replay/delete 和跨租户隔离（`TestLocalStorageServiceSharedStoreIsReadAuthority`、`TestLocalVectorStoreServiceSharedStoreIsReadAuthority`）。
- [x] 为现有 v1 存储资源增加 typed Get/List/child aggregation Store 方法：volume/filesystem/object/bucket/lifecycle/snapshot/mount-target + `VectorStoreResourceStore`（含 KB-link）；filesystem attachment 仍随 filesystem enrich，未独立 Store 表读写接口。
- [x] persistent Store 存在时 GET/LIST 直读 Store：上述资源已切；MinIO 对象浏览与 signed URL 仍走 ObjectStore。
- [x] Provider create 前提交 pending：volume/bucket/snapshot/mount-target/vector（有 backend 时）已按 pending→apply→回写。
- [x] MinIO 保持对象内容和浏览权威；Milvus 保持 embedding/collection 数据权威；PG 保存控制面定义和摘要。
- [x] local profile 无 Store 时保留内存行为。
- [x] 跑 storage/vector store、service 聚焦测试通过；Gateway `DATABASE_URL` 接线留给 B4。

### Task B4：完成存储真实 Provider 行为和重启 gate

**文件：**
- 修改：`repo/services/ani-gateway/storage_runtime.go`
- 修改：`repo/services/ani-gateway/storage_runtime_test.go`
- 修改：`repo/services/ani-gateway/vector_store_runtime.go`
- 修改：`repo/services/ani-gateway/vector_store_runtime_test.go`
- 创建：`repo/deploy/real-k8s-lab/storage-control-plane-state-live-gate.yaml`
- 创建：`repo/scripts/validate_storage_control_plane_state_live_gate.py`
- 创建：`repo/scripts/validate_storage_control_plane_state_live_gate_test.py`
- 创建：`repo/development-records/storage-control-plane-state-a.md`
- 创建（执行后）：`repo/development-records/live-evidence/storage-control-plane-state-live-20260803.json`

- [x] real storage/vector profile 缺 DATABASE_URL、schema 不完整或 PG 不可达时 Gateway 启动失败（runtime 单测 + `storage_control_plane_runtime.go`）。
- [x] live gate 契约与 runner 覆盖最小 volume/snapshot/filesystem/mount-target/bucket/object/vector/KB-link 图（`validate-storage-control-plane-state-live-gate`）。
- [x] Gateway rollout 后按原 ID/list 查询全部资源与关系（live passed；evidence `storage-control-plane-state-live-20260803.json`）。
- [x] 重放原 key 不增加重复资源；同 key 不同 intent 返回冲突（live passed）。
- [x] 删除后 API 隐藏资源，PG 保留墓碑，Provider 临时资源清理为零（live passed）。

---

## 工作流 C：网络

### Task C1：提交 `NETWORK-P0-CONTRACT-A` 纯契约 PR

**文件：**
- 修改：`repo/api/openapi/v1.yaml`
- 修改：`repo/api/core-v1-compatibility-baseline.yaml`
- 修改：`repo/scripts/validate_openapi_spec_test.py`
- 修改：`repo/services/docs/console-modules/compute/network/load-balancer.md`
- 修改：`repo/services/tasks/modules/prd/console/compute/prd-console-network-load-balancer.md`
- 修改：`repo/services/tasks/modules/spec/console/compute/spec-console-network-load-balancer.md`
- 生成：Core SDK 与静态 API 文档生成物

**契约产出：**
- VPC create/response 增加可选 `description` 和只读 `subnet_count`。
- Subnet create/response 增加可选 `zone`，response 增加只读 `available_ip_count`。
- SG create/response 增加可选 `vpc_id`，列表增加可选 `vpc_id` 过滤；7.29 Console 创建流程始终传入，既有 v1 客户端仍可省略。
- SG response 增加只读 `rule_count/bound_instance_count`，用于 7.29 原型列表字段；保留既有 `rules`。
- SG rule 保持历史 `cidr` 字符串字段为 required；不引入 7.29 原型未要求的安全组/实例 peer，兼容性 baseline 不得掩盖 required 字段删除。
- Route request/response 增加可选 `priority`；response/filter 的 next-hop 增加 `local`，create request 仍禁止创建 local route。
- LB 增加只读 `listener_count/backend_count`；新增 listener、backend-group、backend-member typed schemas 和 CRUD 路径。
- 新增 listener 必须通过 `backend_group_id` 关联后端组；历史父资源 `listeners[]` 摘要只兼容新增可选字段，update 可选传入以支持改绑。算法为 `round_robin/weighted_round_robin`，健康检查包含 protocol、port、path、interval_seconds、timeout_seconds、healthy_threshold、unhealthy_threshold。
- backend member 增加只读 `health_status=unknown/healthy/unhealthy`，与资源生命周期 `state` 分离。
- 公网 LB 在没有 EIP 能力时返回 422；不新增虚假的 EIP 占位资源。
- 7.29 原型未要求 UDP、HTTPS 证书、expected status code、静态 VIP 或 EIP 资源字段，本批不预建。
- 同步 LB PRD/SPEC/模块文档：listener/backend 管理进入 P0，不再保留首版只读或 TODO-YAML 表述。

- [x] 调整契约测试并先确认失败，覆盖 SG VPC/聚合字段、历史 `cidr` required、local route 只读、listener 后端组关联、member 健康状态、LB 子资源 CRUD、幂等和错误响应。
- [x] 修改 v1 和 LB 需求文档；只做兼容新增，并纠正当前工作区中的 SG required 退化。
- [x] 重新生成兼容性 baseline、Core SDK 和静态 API 文档；baseline 只接受 additive surface。
- [x] 运行 OpenAPI、兼容性、SDK、Mock/API docs、architecture 和 `git diff --check` 门禁。
- [ ] 停止并等待契约 PR 批准；未批准前不得建立对应 PG 列/表。

### Task C2：建立网络独立 PG migration 和 schema gate

**文件：**
- 创建：`repo/deploy/migrations/20260803_002_network_control_plane_state.sql`
- 创建：`repo/scripts/validate_network_control_plane_state.py`
- 创建：`repo/scripts/validate_network_control_plane_state_test.py`
- 修改：`repo/Makefile`

- [ ] migration 覆盖 VPC、Subnet、IP allocation、SG、rule、binding、route、LB、listener、backend group、backend member。
- [ ] 所有表使用 tenant-first key、复合外键、forced RLS、soft delete 和 create idempotency/fingerprint。
- [ ] 修复 route RLS session key 为 `app.current_tenant_id`，validator 明确拒绝 `ani.tenant_id`。
- [ ] CIDR 使用 `CIDR/INET` 类型；同事务校验 VPC overlap、Subnet containment 和 active overlap。
- [ ] legacy SG rules/LB listeners 可验证 backfill；异常数据阻止 migration。
- [ ] 运行 schema validator、临时 PG migration 测试和 `git diff --check`。
- [ ] 停止等待真实 PG migration 人工批准。

### Task C3：让 Network Store 和 Service 以 PG 为权威

**文件：**
- 修改：`repo/pkg/ports/network_resources.go`
- 修改：`repo/pkg/adapters/runtime/network_store.go`
- 修改：`repo/pkg/adapters/runtime/network_store_test.go`
- 修改：`repo/pkg/adapters/runtime/network_service.go`
- 修改：`repo/pkg/adapters/runtime/network_service_test.go`
- 修改：`repo/services/ani-gateway/network_runtime.go`
- 修改：`repo/services/ani-gateway/network_runtime_test.go`

- [ ] 写两个 Service 实例共享 Store 的失败测试，覆盖全部主/子资源、游标、聚合计数、重放和跨租户隔离。
- [ ] persistent Store 模式下每次 GET/LIST 查询 PG；overview、VPC subnet_count、Subnet available_ip_count 从 PG 聚合。
- [ ] CIDR 冲突返回现有 400/409；有关联的 VPC/Subnet/SG/LB 删除返回 409。
- [ ] Provider create 前写 pending；apply/observe 后回写终态；Provider 调用不占用长事务。
- [ ] real network profile 缺 PG 或 schema 时 Gateway 启动失败；local profile 保留内存行为。
- [ ] 跑 network store/service/provider/router 全量聚焦测试。

### Task C4：实现基础 LB 后端闭环

**文件：**
- 修改：`repo/pkg/ports/network_resources.go`
- 修改：`repo/pkg/adapters/runtime/kubeovn_network_provider.go`
- 修改：`repo/pkg/adapters/runtime/kubeovn_network_provider_test.go`
- 修改：`repo/services/ani-gateway/internal/router/network_resources.go`
- 修改：`repo/services/ani-gateway/internal/router/network_resources_test.go`

- [ ] 先写失败测试覆盖 listener、backend group、instance/IP member、weight、health-check 和安全组端口 precheck。
- [ ] renderer 只接收 ANI 产品意图，不把 Kubernetes Service/EndpointSlice 对象泄漏到 port。
- [ ] provider apply 使用稳定 tenant/resource ID 命名；重复 apply 不创建第二套资源。
- [ ] health 状态由 provider observe 回写 PG，不由 Gateway 内存定时器保存。
- [ ] 公网方案无 EIP 时返回 422，内网 VIP 完成最小闭环。

### Task C5：执行 `NETWORK-PRIVATE-VPC-LIVE-A`

**文件：**
- 创建：`repo/deploy/real-k8s-lab/network-private-vpc-live-gate.yaml`
- 创建：`repo/scripts/validate_network_private_vpc_live_gate.py`
- 创建：`repo/scripts/validate_network_private_vpc_live_gate_test.py`
- 创建：`repo/development-records/network-private-vpc-live-a.md`
- 创建（执行后）：`repo/development-records/live-evidence/network-private-vpc-live-20260803.json`

- [ ] 静态 gate 要求 VPC/Subnet/SG/rule/route/LB、Gateway rollout、tenant isolation 和 cleanup proof。
- [ ] 经人工确认后创建两个私有子网和最小实例，验证同子网、跨子网、自定义 route、SG allow/deny。
- [ ] 验证 internal LB listener/backend/health 和业务端口可达。
- [ ] Gateway rollout 后原网络 ID、关系和实例网络摘要保持一致。
- [ ] 清理全部临时实例、Service、Kube-OVN 对象和 PG 活跃行，保留脱敏墓碑证据。

---

## 工作流 D：实例 `INSTANCE-P0-CLOSURE-A`

### Task D1：提交实例最小契约增量 PR

**文件：**
- 修改：`repo/api/openapi/v1.yaml`
- 修改：`repo/api/core-v1-compatibility-baseline.yaml`
- 修改：`repo/scripts/validate_openapi_spec_test.py`
- 生成：Core SDK 与静态 API 文档生成物

**契约产出：**
- `listInstances.keyword` 描述扩展为名称、ID、描述或私网 IP，保持同一参数不新增重复搜索接口。
- `InstanceLifecycleRequest.action` 增加 `reset_credentials` 和 `update_labels`。
- `reset_credentials` 使用现有 Secret 引用：`ssh_key_ref` 或新增可选 `password_secret_ref`，二者至少一个且不得返回明文。
- `update_labels` 增加可选 `labels` map，采用全量目标状态覆盖语义，重复 apply 幂等。
- 不新增批量实例实体；Console 的 1-N 创建和批量动作使用多个独立幂等请求。

- [ ] 写失败契约测试覆盖 enum、Secret 引用、labels、400/409/422 和不返回明文约束。
- [ ] 修改契约、兼容性基线和生成物并运行全部契约门禁。
- [ ] 停止等待契约 PR 批准；批准前不得实现新 action。

### Task D2：实现实例契约增量和依赖门禁

**文件：**
- 修改：`repo/pkg/ports/workload_runtime.go`
- 修改：`repo/pkg/adapters/runtime/instance_service.go`
- 修改：`repo/pkg/adapters/runtime/instance_service_test.go`
- 修改：`repo/pkg/adapters/runtime/instance_resource_resolver.go`
- 修改：`repo/services/ani-gateway/internal/router/instances.go`
- 修改：`repo/services/ani-gateway/internal/router/instances_test.go`

- [ ] 私网 IP keyword 在 PG 查询层执行，不先加载全租户实例到内存过滤。
- [ ] reset credentials 只把 Secret ref 传给 VM provider，operation before/after 不记录 Secret 明文。
- [ ] update labels 写入实例 PG 目标状态并触发 provider reconcile；同 key/同 intent 返回原 operation。
- [ ] 创建前统一执行镜像 scan/purpose、网络存在性、存储存在性和 GPU spec/inventory precheck。
- [ ] 依赖不可达或状态不满足返回现有 422/503，不降级使用 demo 资源。

### Task D3：完成 GPU Container 统一实例 live gate

**文件：**
- 创建：`repo/deploy/real-k8s-lab/instance-gpu-container-live-gate.yaml`
- 创建：`repo/scripts/validate_instance_gpu_container_live_gate.py`
- 创建：`repo/scripts/validate_instance_gpu_container_live_gate_test.py`
- 创建（执行后）：`repo/development-records/live-evidence/instance-gpu-container-live-20260803.json`

- [ ] 使用 Harbor `purpose=gpu` 的固定 digest 镜像和现有 GPUSpec 创建 GPU Container。
- [ ] 验证 Volcano/HAMi 或 dedicated GPU 调度结果、Pod Ready、GPU inventory 占用和 DCGM 指标。
- [ ] 验证重启、scale、update_image、Exec、operation timeline 和 Gateway rollout 后查询。
- [ ] 删除后释放 GPU inventory、网络绑定、卷挂载和 Provider 资源。

### Task D4：执行四模块最终实例闭环

**文件：**
- 创建：`repo/deploy/real-k8s-lab/instance-p0-resource-closure-live-gate.yaml`
- 创建：`repo/scripts/validate_instance_p0_resource_closure_live_gate.py`
- 创建：`repo/scripts/validate_instance_p0_resource_closure_live_gate_test.py`
- 创建：`repo/development-records/instance-p0-closure-a.md`
- 创建（执行后）：`repo/development-records/live-evidence/instance-p0-resource-closure-live-20260803.json`

- [ ] Gate 前置检查 A4、B4、C5 evidence 均为 passed，任何一个缺失都拒绝执行。
- [ ] VM 使用 system 镜像、私有 VPC/SG、系统盘和数据盘；验证 VNC、启停、凭据重置、终止保护和删除。
- [ ] Container 使用 container 镜像、私有 VPC、卷和文件系统；验证 scale、update image、Exec 和日志/事件/指标。
- [ ] GPU Container 使用 gpu 镜像和 GPUSpec；Sandbox 使用 sandbox 镜像和已有 checkpoint/runtime 能力。
- [ ] Gateway rollout 后四类实例、operation、网络摘要、存储 attachment 和镜像 digest 均保持一致。
- [ ] 重放创建 key 不增加 PG 或 Provider 资源；改变 intent 返回冲突。
- [ ] 清理所有临时资源并证明 Provider 活跃资源为零、PG 仅保留预期墓碑/审计记录。

---

## 文档和总门禁

### Task E1：逐模块归档并执行完整验证

**文件：**
- 修改：`repo/development-records/README.md`
- 修改：`repo/CURRENT-SPRINT.md`
- 修改：`ANI-06-开发计划.md`
- 按模块创建：A4、B4、C5、D4 中声明的 development records

- [ ] 每个模块只在自己的 live gate 通过后标记完成；禁止提前勾选下游实例 closure。
- [ ] 运行所有新增 validator 单元测试和静态 gate。
- [ ] 运行 `make validate-openapi-spec`、`make validate-core-api-compatibility`、`make validate-services`。
- [ ] 运行 `PATH=/tmp/ani-pybin:$PATH make test`、`make validate-architecture`、`make validate-doc-entrypoints`、`git diff --check`。
- [ ] 扫描 diff/evidence，确认没有 Token、密码、私网完整端点、Secret、预签名 URL 或对象内容。
- [ ] 提交前展示每个模块的精确 diff、测试结果、live evidence 和已知边界，等待人工确认。

## 人工确认点

1. **计划确认：** 本文件评审通过后才开始 A1/B1/C1/D1 的契约修改。
2. **契约确认：** 存储、网络、实例的纯契约 PR 独立批准后，才开始该模块对应实现；镜像经 A1 证明现有契约足够后直接实施。
3. **迁移确认：** B2、C2 DDL 和 backfill 报告评审后，才允许应用真实 PG。
4. **真实写确认：** A4、B4、C5、D3、D4 每次 live 写入前单独确认。
5. **发运确认：** 每个模块 commit/push/PR 均独立确认，只能在 `main` 时遵循用户指定的 main 流程。

## 完成标准

- 镜像：真实 push/scan/用途门禁/实例引用/删除保护闭环。
- 存储：PG 权威控制面，Gateway 重启后卷、文件、桶、向量和任务一致。
- 网络：P0 契约、PG 权威、私有 VPC、SG、route、internal LB 真实闭环。
- 实例：VM、Container、GPU Container、Sandbox 使用真实镜像/网络/存储依赖，Gateway 重启后状态一致。
- 所有结论均有脱敏 evidence；未覆盖的 BOSS quota/GC、SMB、复杂混合路由、完整 ACL、跨区域能力继续明确排除。
