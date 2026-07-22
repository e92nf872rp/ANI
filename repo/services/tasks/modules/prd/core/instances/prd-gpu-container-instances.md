# PRD: GPU 容器实例管理

## 1. Introduction/Overview

为 AI 专有云提供 GPU 容器实例的 CRUD REST API（Core OpenAPI），同时提供 Console 租户管理页面和 BOSS 运维管理页面，使租户用户可以通过 Console 自助创建、查询和删除 GPU 容器实例，平台运维管理员可以通过 BOSS 面板全局监控和管理所有 GPU 实例。实例底层由 Kubernetes 调度，通过 WorkloadProvider 抽象层管理 GPU 资源。

API 层、Console 前端和 BOSS 前端在同一 PRD 中规划，API 由 Core 批次实现，Console/BOSS 由各自前端批次实现，三者共享同一组 User Stories 和 Acceptance Criteria。

**[Assumption]** 实例创建时通过 `kind=GPUContainer` 区分于标准 Container 实例，GPU 规格在创建请求中通过 `gpuSpec` 字段指定。

## 2. Goals

- 提供 `/api/v1/instances` 端点，支持 `kind=GPUContainer` 的实例生命周期管理
- 创建实例时支持指定 GPU 型号、GPU 数量、显存大小
- 实例状态机：Pending → Running → Stopped → Deleted（含软删除可恢复）
- 所有创建/删除操作支持 `idempotency_key` 幂等键
- 覆盖 Core OpenAPI 契约定义，通过 `make test` 和 `make validate-architecture`

## 3. User Stories

### US-001: 创建 GPU 容器实例

**Description:** 作为租户用户，我可以通过 API 提交 GPU 容器实例的创建请求，指定 GPU 规格和基础容器参数，这样我能获得一台带有 GPU 算力的运行环境。

**Acceptance Criteria:**
- [ ] `POST /api/v1/instances?kind=GPUContainer` 接受请求，返回 201 Created
- [ ] 请求体包含 `name`, `image`, `gpuSpec`（gpuModel, gpuCount, memoryMB）
- [ ] 支持 `idempotency_key` 请求头，重复调用返回相同实例
- [ ] 返回实例 ID、当前状态（Pending）和创建时间
- [ ] 不支持的 GPU 型号返回 400 BadRequest（含具体错误码）
- [ ] GPU 数量超限（超过配额）返回 403 Forbidden
- [ ] 通过 `make test` 和 `make validate-architecture`

### US-002: 查询 GPU 容器实例列表

**Description:** 作为租户用户，我可以通过列表 API 查看我名下所有 GPU 容器实例的状态，按名称或状态过滤，这样我能掌握实例的运行情况。

**Acceptance Criteria:**
- [ ] `GET /api/v1/instances?kind=GPUContainer` 返回列表，支持 `limit` 和 `offset` 分页
- [ ] 支持查询参数 `name`（模糊匹配）和 `status`（精确匹配）过滤
- [ ] 返回每个实例的 ID、名称、状态、GPU 规格、创建时间
- [ ] 租户隔离：只能看到自己创建的实例（由 tenant 上下文保证）
- [ ] 空列表返回 `{"instances": [], "total": 0}`，不返回 404
- [ ] 支持 `limit` 默认值 20，最大值 100，超限返回 400

### US-003: 查询单个 GPU 容器实例详情

**Description:** 作为租户用户，我可以通过实例 ID 获取单个 GPU 容器实例的完整信息，包括 GPU 规格、状态、事件等，这样我能诊断实例的问题。

**Acceptance Criteria:**
- [ ] `GET /api/v1/instances/{instanceId}` 返回实例详情
- [ ] 返回字段包含：id, name, kind, status, gpuSpec, image, events, createdAt, updatedAt
- [ ] 实例不存在返回 404 NotFound
- [ ] 状态为 Deleted 的已删除实例返回 410 Gone（软删除后可通过恢复接口恢复）
- [ ] 响应体符合 OpenAPI 定义的 InstanceResponse schema

### US-004: 软删除 GPU 容器实例

**Description:** 作为租户用户，我可以通过 API 删除 GPU 容器实例，删除为软删除（实例标记为 Deleted 状态，资源逐步释放），支持在删除延迟期内恢复实例，这样我能避免因误操作丢失实例。

**Acceptance Criteria:**
- [ ] `DELETE /api/v1/instances/{instanceId}` 将实例状态改为 Deleted
- [ ] 支持 `recoveryWindow` 参数（秒），默认 86400（24 小时），最长 604800（7 天）
- [ ] 删除后实例进入删除等待期，期间 `GET` 返回 410 Gone
- [ ] 删除等待期内通过 `PATCH /api/v1/instances/{instanceId}/restore` 可恢复
- [ ] 删除等待期结束后自动触发硬删除，释放 GPU 资源
- [ ] 正在删除中的实例不允许重复删除请求，返回 409 Conflict
- [ ] 所有删除操作支持 `idempotency_key`

### US-005: GPU 实例状态查询与事件追踪

**Description:** 作为租户用户，我能通过实例详情查看实例的创建/运行/删除事件日志，这样我能了解实例的生命周期变化。

**Acceptance Criteria:**
- [ ] 实例详情响应中包含 `events` 数组，按时间倒序排列
- [ ] 每个事件包含：eventID, type（Created/Running/Stopped/Deleted/Failed）、message、timestamp
- [ ] 实例状态变更时自动追加事件记录
- [ ] 事件最多保留 100 条，超出按 FIFO 清理

## 4. Functional Requirements

- FR-1: 在 `repo/api/openapi/v1.yaml` 中新增 `kind=GPUContainer` 的实例创建端点
- FR-2: `POST /api/v1/instances` 的 GPUContainer 请求体必须包含 `gpuSpec` 字段（gpuModel: string enum, gpuCount: integer 1-8, memoryMB: integer）
- FR-3: 实现 GPUInventory adapter，通过 K8s 节点 label 发现可用 GPU 资源
- FR-4: 实现 `WorkloadProvider` 接口，将 GPUContainer 实例映射到底层 K8s Job/Pod 创建
- FR-5: 实例状态机必须遵循：Pending → Running → Stopped → Deleted → (可恢复) → HardDeleted
- FR-6: 所有 POST/DELETE 端点必须支持 `Idempotency-Key` 请求头，Redis 存储幂等键状态
- FR-7: 列表查询必须通过 `pkg/ports/TenantContext` 获取当前租户 ID，实现租户隔离
- FR-8: 删除操作必须记录删除时间戳和恢复窗口，由后台定时任务清理过期删除实例
- FR-9: GPU 型号枚举值至少包含：NVIDIA-A100, NVIDIA-A800, NVIDIA-V100, NVIDIA-4090
- FR-10: 实例创建失败时（GPU 资源不足、镜像拉取失败等）必须返回具体错误码和消息

## 5. Non-Goals (Out of Scope)

- 不包含 GPU 实例的启动/停止/重启操作（仅创建和删除）
- 不包含 GPU 实例的规格变更（resize）
- 不包含多 GPU 实例（单实例多卡）
- 不包含 GPU 共享（如 MIG）
- 不包含 model-service / kb-service 集成
- 不包含 gRPC 接口（仅 REST OpenAPI）
- 不包含 GPU 监控/指标采集
- 不包含节点级 GPU 调度策略（亲和性/反亲和性）

## 6. Design Considerations

- 复用现有 `pkg/ports/WorkloadRuntime` 抽象，GPUContainer 通过 `kind=GPUContainer` 区分
- GPU 发现通过 `pkg/ports/GPUInventory` 抽象层，adapter 实现具体 K8s GPU 设备发现
- 状态机使用有限状态机模式，状态变更必须有原因（cause）和来源（fromState）
- Console 前端使用 TDesign React + TanStack Router 技术栈
- 错误码体系遵循 `repo/api/openapi/v1.yaml` 已有的 Error 定义

## 7. Technical Considerations

- 依赖 KubeVirt 或标准 Container Runtime 的 K8s 集群（通过 `REAL-K8S-LAB-A` 门禁验证）
- 幂等键存储使用 Redis，TTL 设为恢复窗口 + 额外 1 小时
- 后台清理任务使用 Cron，每 5 分钟扫描一次已过期删除实例
- 遵循 ports/adapters 架构：handler 在 `pkg/ports/`，K8s 适配在 `pkg/adapters/`
- 禁止在 handler 中直接导入 K8s SDK，必须经过 port 抽象
- 遵循 ANI 强制规则：不修改 `v1.yaml` 中已有路径，只新增

## 8. Success Metrics

- 创建请求平均响应时间 < 200ms（不含底层资源分配时间）
- 列表查询支持 1000 个实例的毫秒级分页
- 软删除实例在恢复窗口内恢复成功率 100%
- 所有 API 路径通过 `make test` 和 `make validate-architecture`
- OpenAPI 契约与实现零偏差

## 9. Open Questions

- GPU 实例创建时是否需要支持自定义启动命令和挂载卷？（当前 PRD 暂不包含，留待后续）
- 实例删除后 GPU 资源释放是立即触发还是等待 Pod 自然终止？
- 租户 GPU 配额管理是否复用现有的 Quota API，还是需要新建？
- 删除等待期到期后自动硬删除，是否需要异步通知租户？

## 10. ANI Boundaries

| Item | Value |
|------|-------|
| Product line | core + console |
| Code scope | Core: `repo/`（handler, port, adapter, OpenAPI, tests）<br>Console: `repo/frontends/console/`（实例管理页面） |
| OpenAPI authority | Core change batch（需修改 `repo/api/openapi/v1.yaml`） |
| Frozen exclusions | Services backend（model-service, kb-service）, BOSS frontend |
| idempotency_key | required on: POST /api/v1/instances, DELETE /api/v1/instances/{id} |
| Module main doc | `repo/services/docs/console-modules/` |
