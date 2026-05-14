# ANI · 当前冲刺上手指南

> **新开发者（人类或 AI 工具）的第一个入口文件。**
> 读完本文件，5 分钟内明确：当前做什么、怎么开始、怎么验证完成。
> 每冲刺结束时由该冲刺负责人更新。

---

## 当前冲刺

| 字段 | 值 |
|---|---|
| **冲刺编号** | Sprint 1 |
| **时间范围** | 2026-05-15 → 2026-05-31 |
| **主题** | 操作语义底座 + Foundation |
| **核心批次** | M1-INSTANCE-T（最高优先，其他批次可并行）|
| **前置验证** | `make build && make test` 通过 ✅ (83 tests, 2026-05-15) |

---

## P0：M1-INSTANCE-T（操作语义横切基础）

**为什么 P0：** Start/Stop/Resize/Delete 等所有生命周期操作的 precheck/operation_id/timeline 都依赖此批次，后续 VM depth（Sprint 2）和 Console（Sprint 8）都阻塞于此。

### 技术入口

```
pkg/ports/workload_runtime.go      ← 现有接口（13个 interface）
pkg/ports/reconcile_controller.go  ← 新建的 Reconcile port
pkg/adapters/runtime/              ← 现有实现（参考模式）
deploy/migrations/20260502_002_operations_idempotency.sql  ← DB 表已建
api/openapi/v1.yaml (L745-784)     ← /instances/{id}/operations 端点已定义
```

### 实现顺序

```
Step 1  pkg/ports/ 新建 WorkloadOperationStore interface
        RecordOperation / GetOperation / ListOperations / UpdateOperationStep

Step 2  pkg/adapters/runtime/ 新建 MetadataOperationStore
        写入 workload_instance_operations + operation_steps 表（遵守 RLS）

Step 3  修改 WorkloadInstanceService 所有生命周期方法
        - 操作前 RecordOperation(status=accepted)，返回 operation_id
        - provider 执行中 AddStep(各阶段 running→succeeded/failed)
        - 操作失败时写 failure_reason + retry_eligible

Step 4  ani-gateway 新增两个 handler
        GET /instances/{id}/operations   → ListOperations
        GET /instance-operations/{id}    → GetOperation

Step 5  验证 idempotency_key 逻辑
        - DB 唯一索引已在 migration 002 中创建
        - 冲突时返回 409 + 已有 operation_id（不重复创建）
```

### 验收命令

```bash
make test
# 新增测试须覆盖：
# - Create 返回非空 operation_id
# - 同 idempotency_key 重复 Create → 相同 operation_id，实例数不增加
# - GET /instances/{id}/operations → 包含 steps 的操作列表
```

---

## P1：并行批次（本冲刺内完成）

### M1-HEALTH-A（1天）— 所有服务加健康检查端点

```go
// /healthz → liveness（进程活着即 200）
GET /healthz → {"status":"ok","version":"v0.8.0"}

// /readyz → readiness（所有依赖可达才 200）
GET /readyz → {"status":"ok","checks":{"postgres":{"status":"ok","latency_ms":3},...}}
```

4 个服务都要加：ani-gateway / auth-service / model-service / task-service

验收：`curl http://localhost:8080/healthz` → `{"status":"ok",...}`

---

### M1-IDEM-A（3天）— 幂等性令牌集成

Port 字段已加（WorkloadInstanceCreateRequest.IdempotencyKey ✅），需要：
- MetadataOperationStore 检测 DB 唯一索引冲突
- 冲突时返回 409 + 已有 operation_id
- 测试：同 key 重复提交返回相同结果

---

### M2.2-AUTH-FINAL（3天）— Auth 生产收尾

- Dex OIDC 全流程在测试环境端到端通过
- API Key scope 字段验证（schema 在 migration 002 中）
- 参考：`services/auth-service/internal/service/`

---

## 代码结构 10 分钟导航

```
必读（按顺序，理解架构）：
  1. CLAUDE.md                           → 所有约定（5分钟速读）
  2. pkg/ports/workload_runtime.go       → 13个核心接口
  3. pkg/adapters/runtime/instance_orchestrator.go → 编排链路实现
  4. deploy/migrations/20260501_001_init_schema.sql → DB schema
  5. api/openapi/v1.yaml                 → API 契约

查具体实现时参考：
  - 已完成批次记录：repo/development-records/*.md
  - 实例创建全链路：pkg/adapters/runtime/instance_orchestrator.go
  - Kubernetes adapter：pkg/adapters/runtime/kubernetes_rest_client.go
  - Bootstrap/wiring：pkg/bootstrap/deps.go
```

---

## 环境启动

```bash
cd /path/to/ANI/repo

# 1. 启动依赖（PostgreSQL / NATS / Redis / MinIO / Milvus）
make deps

# 2. 验证
make build && make test

# 3. 热重载开发
make dev-gateway   # 另开终端

# 4. 运行特定测试
GOCACHE=/private/tmp/ani-go-build GOMODCACHE=$(pwd)/.cache/gomod \
  go test ./pkg/adapters/runtime/... -v -run TestM1

# 5. 提交前
make validate-architecture && make test
```

---

## Sprint 1 完工标准（2026-05-31 前必须全通）

```bash
# 基础
make test                        # 全通（含新增操作测试）
curl /healthz → {"status":"ok"}
curl /readyz  → {"status":"ok","checks":{...}}

# 功能（make deps 后执行）
POST /api/v1/instances {..., "idempotency_key":"uuid-A"}  → 返回 operation_id
POST /api/v1/instances {..., "idempotency_key":"uuid-A"}  → 同一 operation_id，不新增实例
GET  /api/v1/instances/{id}/operations                   → steps 列表非空
```

---

## 下一冲刺预览（Sprint 2，2026-06-01）

**主题：VM & Container/GPU 生产深度**
- M1-INSTANCE-U：VM VNC / 快照 / 磁盘绑定
- M1-INSTANCE-V：Container rollout/rollback + GPU 调度原因展示

进入条件：Sprint 1 完工标准全通。

---

## 约定速查

| 约定 | 规则 |
|---|---|
| 接口 | 先更新 `api/openapi/v1.yaml`，再写实现 |
| 测试 | 每个公开函数需有测试，不写测试不 merge |
| SDK 调用 | 不直接调 K8s/KubeVirt SDK，通过 ports 接口 |
| RLS | 所有 DB 写入前必须设置 `app.tenant_id` |
| "API 契约" | 不说"OpenAPI"（避免与 OpenAI 混淆）|

## 完工后必做（每个批次完成时）

```
① make test              → 全通
② 新建 development-records/{批次名}.md  → 用 TEMPLATE.md 模板
③ development-records/README.md         → 追加一行到对应分组表格
④ 本文件（CURRENT-SPRINT.md）          → 该批次状态 🔄 → ✅，下一批次 ⏳ → 🔄
⑤ ANI-06-开发计划.md Section 零        → 更新批次/Sprint 状态
⑥ git commit -m "feat: {批次名} {一句话}"
```

**Sprint 全部完成时额外操作：**
```
⑦ ANI-06 Section 零 Sprint 行：🔄 → ✅（填完成日期）/ 下一 Sprint：⏳ → 🔄
⑧ 本文件（CURRENT-SPRINT.md）整体重写为下一个 Sprint 内容
⑨ git commit -m "sprint: Sprint N completed"
```

完整规约说明：`CLAUDE.md` → "📋 开发进度更新规约"

---

*Sprint 1 负责人：[填入]　最后更新：2026-05-15*
*Sprint 结束后更新为下一冲刺内容。*
