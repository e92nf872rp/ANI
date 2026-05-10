# KuberCloud ANI · 代码实现规范

> 版本 V1 | 广州常青云科技有限公司 | 内部技术文档  
> **AI 代码生成必读**：本文档是 GPT/Claude 生成代码的"合同"，所有生成代码必须符合此规范。

---

## 零、开发阶段命名与进度记录约定

这是代码生成的强制约定，优先级高于单次提示词中的临时命名。

### 0.1 产品模块编号

- 产品计划阶段只以 `ANI-06-开发计划.md` 为准。
- `模块 1/2/3...` 表示产品开发模块，不能被代码生成批次复用。
- 当前实现仍处于 `ANI-06 / 模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架`，不是 `模块 3：模型管理平台`。

### 0.2 代码生成批次编号

代码生成批次必须使用以下格式：

```text
M{模块号}.{小节号}-{主题}-{批次字母}
```

示例：

```text
M2.1-TASK-A
M2.1-TASK-B
M2.1-TASK-C
```

禁止在新的记录、提交说明、提示词中继续使用 `Stage 3A/3B/3C` 作为主名称，因为它会被误解为 `ANI-06` 的模块 3。

历史旧名映射：

| 新名称 | 历史旧名 | 产品计划映射 |
|---|---|---|
| `M2.1-TASK-A` | `Stage 3A` | `模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架 / task query path` |
| `M2.1-TASK-B` | `Stage 3B` | `模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架 / transactional outbox + NATS publisher` |
| `M2.1-TASK-C` | `Stage 3C` | `模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架 / worker mutation RPCs` |

### 0.3 进度记录文件

- 每个代码生成批次完成后，必须新增或更新 `repo/development-records/` 下的记录。
- `repo/development-records/README.md` 是当前进度索引，任何阶段推进都必须同步更新。
- 记录文件命名必须使用新命名格式，例如 `m2-1-task-c-worker-mutations.md`。
- 记录中必须包含：产品计划映射、实现范围、修改文件、验证命令、剩余风险、下一步边界。

### 0.4 版本管理

- 版本号、tag、发布包和兼容性判定以 `ANI-12-版本管理策略.md` 为准。
- 任何 API/Proto/DB/CRD/Helm values/NATS payload 的变更，提交说明或开发记录中必须标注兼容性影响：`MAJOR`、`MINOR`、`PATCH` 或 `no-release-impact`。
- Codex Cloud 生成代码时不得擅自创建正式版本 tag。
- 当前开发期默认属于 `v0.x`，只有达到 `ANI-12` 的发布门禁后才能进入 `v1.0.0-rc.N` 或 `v1.0.0`。

---

## 一、模块结构

### 1.1 Go Workspace 布局

```
repo/
├── go.work                          # Go Workspace，统一管理所有模块
├── pkg/                             # 共享包（module: github.com/kubercloud/ani/pkg）
│   ├── types/                       # 跨服务共享类型
│   │   ├── errors.go                # 错误类型、错误码常量、HTTP 状态映射
│   │   ├── tenant.go                # TenantContext、SetDBTenant、FromContext
│   │   └── pagination.go           # CursorPage[T]、EncodeCursor、DecodeCursor
│   ├── nats/
│   │   └── messages.go              # 所有 NATS Subject 的 Payload struct（唯一来源）
│   └── bootstrap/
│       ├── server.go                # gRPC 服务器启动/关闭模板
│       ├── db.go                    # PostgreSQL 连接池（pgxpool，带重试）
│       ├── nats.go                  # NATS JetStream 连接
│       └── redis.go                 # Redis 连接
│
├── api/proto/                       # Protobuf 定义（唯一的 API 权威来源）
│   ├── buf.yaml
│   ├── buf.gen.yaml
│   ├── common/v1/common.proto
│   ├── model/v1/model_service.proto
│   ├── inference/v1/inference_service.proto
│   ├── kb/v1/kb_service.proto
│   ├── auth/v1/auth_service.proto
│   ├── metering/v1/metering_service.proto
│   └── task/v1/task_service.proto
│
└── services/{name}/                 # 每个微服务
    ├── main.go                      # 只调用 bootstrap，10 行以内
    ├── go.mod                       # 独立 module，通过 go.work 关联 pkg
    ├── internal/
    │   ├── config/config.go         # 从环境变量加载，用 pydantic-settings 风格
    │   ├── repo/                    # Repository 层（接口 + PostgreSQL 实现）
    │   ├── service/                 # 业务逻辑层（gRPC Service 实现）
    │   └── worker/                  # NATS 消费者（如有）
    └── internal/repo/               # 接口在此定义，实现也在此
```

### 1.2 每个服务的标准 main.go

```go
package main

import (
    "github.com/kubercloud/ani/pkg/bootstrap"
    "github.com/kubercloud/ani/{service}/internal/config"
    "{service}/internal/service"
)

func main() {
    cfg := config.Load()                    // 从环境变量加载，失败直接 os.Exit(1)
    deps := bootstrap.MustConnect(cfg)      // DB+NATS+Redis，连接失败 os.Exit(1)
    defer deps.Close()

    svc := service.New(deps)
    bootstrap.RunGRPC(cfg.GRPCPort, svc.Register, deps)
}
```

---

## 二、Repository 接口规范

### 2.1 通用约定

```
位置：services/{name}/internal/repo/{resource}_repo.go
命名：{Resource}Repo interface
实现：{Resource}PostgresRepo struct（在同一文件或 {resource}_repo_pg.go 中）
测试：{resource}_repo_test.go（使用 testcontainers-go，非 mock PG）
```

**强制规则：**
- 所有方法第一个参数必须是 `context.Context`
- 所有写操作（Insert/Update/Delete）接收 `pgx.Tx`，由 service 层管理事务
- 所有读操作（Get/List）接收 `*pgxpool.Pool`（或通过 Conn）
- 每个事务开始时必须调用 `types.SetDBTenant(ctx, tx)`（在 Tx 内）
- 不存在时返回 `types.ErrNotFound`（不是 nil, nil）

### 2.2 InferenceService Repository 接口

```go
// 文件：services/inference-service/internal/repo/inference_repo.go
package repo

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/kubercloud/ani/pkg/types"
)

type InferenceServiceRepo interface {
    // Create writes inference_services + outbox_events in a single transaction.
    // The caller must begin the transaction and call SetDBTenant before this.
    Create(ctx context.Context, tx pgx.Tx, spec CreateSpec) (*InferenceService, error)

    // GetByID returns the service for the tenant in ctx. Returns ErrNotFound if absent.
    GetByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*InferenceService, error)

    // List returns cursor-paginated services.
    List(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) ([]*InferenceService, string, error)

    // UpdateStatus updates phase, endpoint_url, ready_replicas, message.
    // Uses optimistic locking: fails if observed_generation != current generation.
    UpdateStatus(ctx context.Context, tx pgx.Tx, update StatusUpdate) error

    // SetEndpoint sets endpoint_url when phase transitions to Running.
    SetEndpoint(ctx context.Context, tx pgx.Tx, id uuid.UUID, url string) error

    // SoftDelete marks status=deleted and removes from routing cache.
    SoftDelete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error

    // GetStaleForReconciliation returns services that appear running in DB
    // but have not had a heartbeat from the Operator within leaseExpiry.
    GetStaleForReconciliation(ctx context.Context, pool *pgxpool.Pool, leaseExpiry time.Duration) ([]*InferenceService, error)
}

// CreateSpec contains the validated fields for a new InferenceService.
type CreateSpec struct {
    TenantID        uuid.UUID
    Name            string
    ModelVersionID  uuid.UUID
    Replicas        int32
    GPUType         string
    GPUCountPerPod  int32
    MaxConcurrency  int32
    PlacementRegion string
    PlacementAZ     string
}

// StatusUpdate carries the fields the Operator reports back.
type StatusUpdate struct {
    ID                 uuid.UUID
    Phase              string
    EndpointURL        string
    Message            string
    ReadyReplicas      int32
    ObservedGeneration int64
}

// ListFilter controls pagination and filtering for List().
type ListFilter struct {
    Status string          // empty = all phases
    Cursor string          // opaque cursor from previous response
    Limit  int             // 0 = default (20)
}

// InferenceService is the domain model (not the proto, not the DB row directly).
type InferenceService struct {
    TenantID        uuid.UUID
    ID              uuid.UUID
    Name            string
    ModelVersionID  uuid.UUID
    Replicas        int32
    GPUType         string
    GPUCountPerPod  int32
    MaxConcurrency  int32
    PlacementRegion string
    PlacementAZ     string
    Phase           string
    EndpointURL     string
    ReadyReplicas   int32
    Message         string
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

### 2.3 AsyncTask Repository 接口（共享，放在 pkg/repo/）

```go
// 文件：pkg/repo/task_repo.go
package repo

import (
    "context"
    "time"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    natsmsg "github.com/kubercloud/ani/pkg/nats"
)

type AsyncTaskRepo interface {
    // Create writes async_tasks + outbox_events atomically within tx.
    // Returns ErrConflict if idempotency_key already exists for this tenant.
    Create(ctx context.Context, tx pgx.Tx, req CreateTaskReq) (*AsyncTask, error)

    // GetByID returns a task visible to the tenant.
    GetByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*AsyncTask, error)

    // GetByIdempotencyKey returns existing task or (nil, nil) if not found.
    GetByIdempotencyKey(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string) (*AsyncTask, error)

    // AcquireLease atomically sets lease_until if not already leased.
    // Returns (true, leaseUntil) on success; (false, zero) if already leased.
    AcquireLease(ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID, duration time.Duration) (bool, time.Time, error)

    // Heartbeat refreshes last_heartbeat_at to prevent lease expiry.
    Heartbeat(ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID) error

    // UpdateProgress sets progress_pct.
    UpdateProgress(ctx context.Context, pool *pgxpool.Pool, taskID uuid.UUID, pct int) error

    // Complete marks status=completed, clears lease, writes result JSON.
    // Also writes a TaskCompletedEvent to outbox_events within tx.
    Complete(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, result any) error

    // Fail increments attempt_count; if >= max_attempts, sets status=dead_letter.
    // Also writes a TaskCompletedEvent(failed) to outbox_events within tx.
    Fail(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, errMsg, compensatingAction string) error

    // GetExpiredLeases returns tasks in Running state with expired leases,
    // used by the reconciler goroutine.
    GetExpiredLeases(ctx context.Context, pool *pgxpool.Pool, limit int) ([]*AsyncTask, error)
}

type CreateTaskReq struct {
    TenantID       uuid.UUID
    IdempotencyKey string
    TaskType       string
    ResourceType   string
    ResourceID     uuid.UUID
    MaxAttempts    int
    WebhookURL     string
    // OutboxSubject and OutboxPayload define the NATS message to publish via Outbox.
    OutboxSubject  string
    OutboxPayload  any   // will be JSON-marshalled into outbox_events.payload
}

type AsyncTask struct {
    TenantID          uuid.UUID
    ID                uuid.UUID
    IdempotencyKey    string
    TaskType          string
    ResourceType      string
    ResourceID        uuid.UUID
    Status            string
    AttemptCount      int
    MaxAttempts       int
    ProgressPct       int
    ErrorMessage      string
    CompensatingAction string
    LeaseUntil        *time.Time
    LastHeartbeatAt   *time.Time
    DeadLetterAt      *time.Time
    WebhookURL        string
    CreatedAt         time.Time
    StartedAt         *time.Time
    CompletedAt       *time.Time
}
```

### 2.4 KnowledgeBase Repository 接口

```go
// 文件：services/kb-service/internal/repo/kb_repo.go
type KnowledgeBaseRepo interface {
    Create(ctx context.Context, tx pgx.Tx, req CreateKBReq) (*KnowledgeBase, error)
    GetByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*KnowledgeBase, error)
    List(ctx context.Context, pool *pgxpool.Pool, filter ListFilter) ([]*KnowledgeBase, string, error)
    SoftDelete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
}

type KBDocumentRepo interface {
    Reserve(ctx context.Context, tx pgx.Tx, req ReserveDocReq) (*KBDocument, error)
    SetStoragePath(ctx context.Context, tx pgx.Tx, docID uuid.UUID, path string) error
    UpdateParseStatus(ctx context.Context, tx pgx.Tx, docID uuid.UUID, status string, chunkCount int) error
    GetByID(ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) (*KBDocument, error)
    ListByKB(ctx context.Context, pool *pgxpool.Pool, kbID uuid.UUID, filter ListFilter) ([]*KBDocument, string, error)
    Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
    // GetByIdempotencyKey prevents duplicate uploads (file checksum as key suffix)
    GetByIdempotencyKey(ctx context.Context, pool *pgxpool.Pool, kbID uuid.UUID, key string) (*KBDocument, error)
}
```

---

## 三、三条核心流程时序

### 流程 A：部署推理服务（完整端到端）

```
1. Client POST /api/v1/inference-services
   Headers: Authorization: Bearer <jwt>
   Body: { name, model, replicas, gpu_type, idempotency_key, ... }

2. ANI Gateway Middleware 链顺序执行：
   → RequestID()      → 注入 request_id
   → Auth()           → ValidateToken gRPC → types.WithTenant(ctx, tc)
   → RBAC()           → CheckPermission(resource="inference_services", action="create")
   → RateLimit()      → Redis 令牌桶检查（key: "ratelimit:tenant:{tenantID}")
   → Audit()          → 异步写 audit_logs（channel buffer，不阻塞）

3. Gateway Handler: inferenceHandler.Create(ctx, req)
   a. 参数校验（model 格式 "[a-z0-9.-]+:[a-z0-9.-]+"，replicas 1-32）
   b. taskRepo.GetByIdempotencyKey(ctx, pool, tenantID, req.idempotency_key)
      → 若已存在: 直接返回 202 + 已有 AsyncTask（幂等）
   c. gRPC: inferenceSvcClient.CreateInferenceService(ctx, spec)

4. inference-service.CreateInferenceService() 方法体：
   a. db.BeginTx(ctx)
   b. types.SetDBTenant(ctx, tx)
   c. inferenceRepo.Create(ctx, tx, spec)
      → INSERT inference_services (status=pending)
   d. taskRepo.Create(ctx, tx, CreateTaskReq{
          OutboxSubject:  nats.SubjectInferenceDeploy,
          OutboxPayload:  nats.InferenceDeployMsg{...},
          IdempotencyKey: req.idempotency_key,
      })
      → INSERT async_tasks (status=pending)
      → INSERT outbox_events (published=false)
   e. tx.Commit()
   f. 返回 CreateInferenceServiceResponse{service, task}

5. Gateway 响应：
   HTTP 202 + Location: /api/v1/tasks/{task_id}
   Body: AsyncTask JSON

--- 异步链路 ---

6. outbox_publisher goroutine（每 500ms 运行）：
   SELECT id, aggregate_type, aggregate_id, event_type, payload
   FROM outbox_events WHERE NOT published
   ORDER BY created_at LIMIT 100
   FOR UPDATE SKIP LOCKED
   → js.Publish(subject, payload)
   → UPDATE outbox_events SET published=true, published_at=NOW()

7. NATS Consumer in inference-operator（Subject: ani.tasks.inference.deploy）：
   a. json.Unmarshal(msg.Data, &deployMsg)
   b. taskClient.AcquireTaskLease(deployMsg.TaskID, 10*time.Minute)
      → 失败（ALREADY_EXISTS）: msg.Nak(); return
   c. 创建 K8s InferenceService CRD（client.Create）
      → 失败: taskClient.FailTask(taskID, err, "delete_crd_if_exists")
               → attempt_count+1; if >= max_attempts: dead_letter + AlertManager
               msg.Nak(); return
   d. msg.Ack()
   e. goroutine: Heartbeat(taskID) every 30s until done

8. K8s Operator Reconcile 循环监听 CRD 变化：
   → 创建 Deployment + Service (with Init Container)
   → Init Container: 下载模型 + 可选解密 → 写入 emptyDir
   → vLLM 启动，/health 返回 200
   → Operator 更新 CRD status.phase = Running

9. Operator Watch 到 CRD phase=Running：
   a. inferenceSvcClient.UpdateStatus(Running, endpoint_url)
      → BEGIN TX
      → types.SetDBTenant(ctx, tx)  [internal tenant, bypass RLS]
      → UPDATE inference_services SET phase='running', endpoint_url=...
      → COMMIT
   b. taskClient.CompleteTask(taskID, result{service_id, endpoint_url})
      → UPDATE async_tasks SET status=completed
      → INSERT outbox_events (TaskCompletedEvent)

10. outbox_publisher 发布 TaskCompletedEvent
    Subject: ani.events.task.completed.{taskID}

11. ANI Gateway SSE handler (if client is listening):
    js.Subscribe(subject) → 推送 SSE event 到前端
    OR Webhook dispatcher: POST to webhook_url
```

### 流程 B：上传文档到知识库（完整端到端）

```
1. Client POST /api/v1/knowledge-bases/{kb_id}/documents (multipart/form-data)
   → 文件由 ANI Gateway 接收（不超过 500MB）

2. Gateway kbHandler.PrepareUpload(ctx, req)：
   a. 计算文件 checksum_sha256（SHA256 of body bytes）
   b. 构造 idempotency_key = "kb-doc:{kb_id}:{checksum}"
   c. gRPC: kbSvcClient.GetDocumentUploadURL(...)
      → docRepo.GetByIdempotencyKey() → 若已存在返回已有文档（幂等）
      → MinIO.PresignedPutObject(bucket, path, 15min)
      → BEGIN TX
      → docRepo.Reserve(tx, req) → INSERT kb_documents(parse_status=pending)
      → COMMIT
      → 返回 {doc_id, upload_url, storage_path}

3. Client PUT {upload_url} （直传 MinIO，不经过 Gateway）

4. Client POST /api/v1/knowledge-bases/{kb_id}/documents/{doc_id}/notify-uploaded

5. Gateway kbHandler.NotifyUploaded(ctx, req)：
   gRPC: kbSvcClient.NotifyDocumentUploaded(doc_id)

6. kb-service.NotifyDocumentUploaded()：
   a. MinIO.StatObject(doc.storage_path) → 验证 ETag == checksum
   b. BEGIN TX
   c. types.SetDBTenant(ctx, tx)
   d. docRepo.SetStoragePath(tx, doc_id, storage_path)
   e. taskRepo.Create(tx, CreateTaskReq{
          OutboxSubject:  nats.SubjectKBParse,
          OutboxPayload:  nats.KBParseMsg{...},
          IdempotencyKey: "kbparse:{doc_id}",
      })
   f. COMMIT
   g. 返回 AsyncTaskRef

7. outbox_publisher 发布 KBParseMsg

8. NATS Consumer in doc-parser (ani.tasks.kb.parse)：
   a. AcquireLease(taskID, 30min)
   b. MinIO.GetObject(storage_path) → stream to Docling/PaddleOCR
   c. chunks = docling.Parse(stream)
   d. taskClient.UpdateProgress(taskID, 50)
   e. 发布 ani.tasks.kb.index（KBIndexMsg{chunks}）
      注意：直接 js.Publish，不走 outbox（解析服务无 DB）
   f. taskClient.UpdateProgress(taskID, 60)

9. NATS Consumer in rag-engine (ani.tasks.kb.index)：
   a. AcquireLease(taskID, 15min)
   b. embeddings = embed_model.encode([c.content for c in chunks])
   c. vectors = [{id: f"{doc_id}_{i}", values: emb, ...} for i, emb in enumerate(embeddings)]
   d. Milvus.upsert(collection=kb_collection_name(kb_id), data=vectors)
      → 主键 f"{doc_id}_{chunk_index}" 保证幂等（重复 upsert = 覆盖）
   e. BEGIN TX (via internal DB client, bypassing tenant RLS for internal ops)
   f. UPDATE kb_documents SET parse_status='ready', chunk_count=N, parsed_at=NOW()
   g. UPDATE knowledge_bases SET doc_count = doc_count + 1
   h. taskRepo.Complete(tx, taskID)
   i. INSERT outbox_events (TaskCompletedEvent)
   j. COMMIT
```

### 流程 C：HuggingFace 模型导入（完整端到端）

```
1. Client POST /api/v1/models/import
   Body: { source, repo_id, revision, idempotency_key, webhook_url }

2. Gateway modelHandler.ImportModel(ctx, req)：
   a. taskRepo.GetByIdempotencyKey() → 幂等检查
   b. gRPC: modelSvcClient.ImportModel(req)

3. model-service.ImportModel()：
   a. BEGIN TX
   b. types.SetDBTenant(ctx, tx)
   c. modelRepo.Create(tx, {status=pending, source=huggingface})
   d. taskRepo.Create(tx, CreateTaskReq{
          OutboxSubject:  nats.SubjectModelImport,
          OutboxPayload:  nats.ModelImportMsg{...},
          IdempotencyKey: req.idempotency_key,
          MaxAttempts:    3,
      })
   e. COMMIT
   f. 返回 AsyncTaskRef

4. outbox_publisher 发布 ModelImportMsg

5. NATS Consumer in model-downloader (ani.tasks.model.import)：
   a. AcquireLease(taskID, 2*time.Hour)  ← 大模型下载时间长
   b. os.MkdirAll(f"/tmp/dl/{model_id}", 0700)
   c. snapshot_download(
          repo_id, revision,
          local_dir=f"/tmp/dl/{model_id}",
          resume_download=True,           ← 断点续传
          endpoint=HF_ENDPOINT,           ← 国内镜像
          token=HF_TOKEN,                 ← optional
      )
   d. 每完成 10% → Heartbeat(taskID) + UpdateProgress(pct)
   e. 计算 total_size_bytes
   f. MinIO.UploadDirectory(
          f"/tmp/dl/{model_id}",
          bucket="ani-models",
          prefix=f"{tenant_id}/{model_id}/main/",
      )
   g. BEGIN TX (internal client, bypass RLS)
   h. INSERT model_versions(version='main', format, storage_path, checksum, size_bytes)
   i. UPDATE models SET status='ready', total_size_bytes=N
   j. taskRepo.Complete(tx, taskID)
   k. INSERT outbox_events (TaskCompletedEvent)
   l. COMMIT
   m. os.RemoveAll(f"/tmp/dl/{model_id}")
```

---

## 四、服务 Bootstrap 模式

### 4.1 Dependencies 结构体

```go
// 文件：pkg/bootstrap/deps.go
package bootstrap

import (
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/nats-io/nats.go"
    "github.com/redis/go-redis/v9"
    "go.opentelemetry.io/otel/trace"
    "log/slog"
)

// Deps holds all external dependencies for a service.
// All fields are non-nil after MustConnect returns (it panics otherwise).
type Deps struct {
    DB     *pgxpool.Pool
    NATS   *nats.Conn
    JS     nats.JetStreamContext
    Redis  *redis.Client
    Tracer trace.Tracer
    Logger *slog.Logger
}

func (d *Deps) Close() {
    d.DB.Close()
    d.NATS.Close()
    d.Redis.Close()
}
```

### 4.2 MustConnect 实现草图

```go
// pkg/bootstrap/connect.go
func MustConnect(cfg Config) *Deps {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
    slog.SetDefault(logger)

    db, err := connectDB(cfg.DatabaseURL)       // retry up to 30s
    if err != nil { slog.Error("db connect failed", "err", err); os.Exit(1) }

    nc, js, err := connectNATS(cfg.NATSURL)     // retry up to 30s
    if err != nil { slog.Error("nats connect failed", "err", err); os.Exit(1) }

    rdb, err := connectRedis(cfg.RedisURL)
    if err != nil { slog.Error("redis connect failed", "err", err); os.Exit(1) }

    return &Deps{DB: db, NATS: nc, JS: js, Redis: rdb, Logger: logger}
}
```

### 4.3 RunGRPC 实现草图

```go
// pkg/bootstrap/server.go
func RunGRPC(port int, register func(*grpc.Server), deps *Deps) {
    lis, _ := net.Listen("tcp", fmt.Sprintf(":%d", port))

    srv := grpc.NewServer(
        grpc.ChainUnaryInterceptor(
            otelgrpc.UnaryServerInterceptor(),
            loggingInterceptor(deps.Logger),
            recoveryInterceptor(),
        ),
    )
    register(srv)
    reflection.Register(srv)   // for grpcurl / grpc-gateway

    go func() {
        if err := srv.Serve(lis); err != nil {
            deps.Logger.Error("grpc serve error", "err", err)
        }
    }()

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    <-ctx.Done()

    deps.Logger.Info("shutting down gRPC server")
    srv.GracefulStop()
}
```

---

## 五、Go 编码规范

### 5.1 错误处理

```go
// ✅ 正确：包装错误时携带调用位置
return nil, fmt.Errorf("inferenceRepo.GetByID id=%s: %w", id, types.ErrNotFound)

// ✅ 正确：在 service 层转换错误为 gRPC status
if errors.Is(err, types.ErrNotFound) {
    return nil, status.Errorf(codes.NotFound, "inference service %s not found", id)
}

// ❌ 错误：丢失 cause
return nil, types.ErrNotFound

// ❌ 错误：在 repo 层直接返回 gRPC status（破坏层次）
return nil, status.Error(codes.NotFound, "...")
```

**HTTP 状态码映射**（Gateway 层统一处理）：

| types 错误 | gRPC 状态 | HTTP 状态 |
|---|---|---|
| ErrNotFound | NotFound | 404 |
| ErrConflict | AlreadyExists | 409 |
| ErrUnauthorized | Unauthenticated | 401 |
| ErrForbidden | PermissionDenied | 403 |
| ErrBadRequest / ErrInvalidState | InvalidArgument | 400 |
| 其他 | Internal | 500（不暴露原因）|

### 5.2 日志规范

```go
// ✅ 每条日志至少含 tenant_id 和 request_id
slog.InfoContext(ctx, "inference service created",
    "tenant_id", tc.TenantID,
    "request_id", types.RequestIDFromContext(ctx),
    "service_id", svc.ID,
    "model", req.Model,
)

// ❌ 禁止打印敏感数据
slog.Info("api key created", "key", keyValue)   // 永不！

// ❌ 禁止无结构日志
log.Printf("error: %v", err)
```

**必须出现在每条日志中的字段：**
- `tenant_id`（从 `types.FromContext(ctx)` 提取）
- `request_id`（从 ctx 或 gRPC metadata 提取）
- `op`（gRPC 方法名，如 `inference.CreateInferenceService`）

### 5.3 Context 和数据库操作

```go
// ✅ 每个 DB 写操作前在事务内设置租户
func (r *inferencePostgresRepo) Create(ctx context.Context, tx pgx.Tx, spec CreateSpec) (*InferenceService, error) {
    if err := types.SetDBTenant(ctx, tx); err != nil {
        return nil, fmt.Errorf("inferenceRepo.Create: %w", err)
    }
    // ... INSERT ...
}

// ✅ 读操作中 pgx 自动使用 RLS（app.current_tenant_id 已由中间件设置）
// 无需手动传 tenant_id 给 WHERE 子句（RLS 自动过滤）

// ❌ 在循环内查询数据库（N+1）
for _, id := range ids {
    svc, _ := r.GetByID(ctx, pool, id)   // 每次独立查询
}

// ✅ 用 pgx batch 批量查询
batch := &pgx.Batch{}
for _, id := range ids {
    batch.Queue("SELECT * FROM inference_services WHERE id=$1", id)
}
results := pool.SendBatch(ctx, batch)
```

### 5.4 NATS 消费者规范

```go
// ✅ 正确的 NATS 消费者结构
func (w *InferenceDeployWorker) consume(msg *nats.Msg) {
    var payload natsmsg.InferenceDeployMsg
    if err := json.Unmarshal(msg.Data, &payload); err != nil {
        slog.Error("invalid message payload", "subject", msg.Subject)
        msg.Nak()   // requeue for retry
        return
    }

    // 1. 获取租约（原子操作）
    acquired, _, err := w.taskRepo.AcquireLease(ctx, pool, payload.TaskID, 10*time.Minute)
    if err != nil || !acquired {
        msg.Nak()   // another worker has the lease; will retry
        return
    }

    // 2. 幂等检查（是否已处理过）
    task, err := w.taskRepo.GetByID(ctx, pool, payload.TaskID)
    if task.Status == "completed" || task.Status == "dead_letter" {
        msg.Ack()   // already done, discard
        return
    }

    // 3. 执行业务逻辑（可以失败，有重试）
    if err := w.deploy(ctx, payload); err != nil {
        w.taskRepo.Fail(ctx, tx, payload.TaskID, err.Error(), "cleanup_k8s_resources")
        msg.Ack()   // consumed; retry will be triggered by reconciler if needed
        return
    }

    msg.Ack()   // 成功后才 Ack
}
```

### 5.5 测试规范

```go
// ✅ Repository 集成测试使用 testcontainers-go
func TestInferenceRepo(t *testing.T) {
    ctx := context.Background()
    pg, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
        ContainerRequest: testcontainers.ContainerRequest{
            Image:        "postgres:17-alpine",
            ExposedPorts: []string{"5432/tcp"},
            Env:          map[string]string{"POSTGRES_PASSWORD": "test"},
            WaitingFor:   wait.ForListeningPort("5432/tcp"),
        },
        Started: true,
    })
    require.NoError(t, err)
    defer pg.Terminate(ctx)

    // 运行迁移
    pool := connectTestDB(t, pg)
    runMigrations(t, pool)

    repo := repo.NewInferencePostgresRepo(pool)

    // 设置 tenant context
    tc := &types.TenantContext{TenantID: uuid.New(), UserID: uuid.New()}
    ctx = types.WithTenant(ctx, tc)

    // 测试
    svc, err := createTestService(ctx, pool, repo, tc.TenantID)
    require.NoError(t, err)
    assert.Equal(t, "pending", svc.Phase)

    // 验证 RLS：另一个 tenant 看不到这条记录
    ctx2 := types.WithTenant(ctx, &types.TenantContext{TenantID: uuid.New()})
    _, err = repo.GetByID(ctx2, pool, svc.ID)
    assert.ErrorIs(t, err, types.ErrNotFound)   // RLS 过滤
}
```

---

## 六、前端路由结构

### 6.1 Console 路由树（TanStack Router 文件路由）

```
frontends/console/src/routes/
├── __root.tsx               全局布局（AppShell：Header + Sidebar + Outlet）
├── index.tsx                仪表盘 (/)
├── models/
│   ├── index.tsx            模型列表 (/models)
│   ├── import.tsx           模型导入向导 (/models/import)
│   └── $modelId/
│       └── index.tsx        模型详情 (/models/:modelId)
├── kb/
│   ├── index.tsx            知识库列表 (/kb)
│   ├── new.tsx              新建知识库 (/kb/new)
│   └── $kbId/
│       ├── index.tsx        知识库详情-文档列表 (/kb/:kbId)
│       └── chat.tsx         知识库问答 (/kb/:kbId/chat)
├── usage.tsx                用量报表 (/usage)
├── registry.tsx             镜像仓库 (/registry)
└── settings/
    ├── index.tsx            设置首页 (/settings)
    └── api-keys.tsx         API Key 管理 (/settings/api-keys)
```

### 6.2 API Client 生成方式

```bash
# 从 OpenAPI Spec 生成 TypeScript 类型（make gen-api 中调用）
npx openapi-typescript ../../api/openapi/v1.yaml -o src/api/schema.d.ts

# 使用方式（类型安全，无手写 URL）
import createClient from 'openapi-fetch'
import type { paths } from './api/schema'

const api = createClient<paths>({ baseUrl: '/api/v1' })

// 使用
const { data, error } = await api.GET('/models', {
    params: { query: { limit: 20 } }
})
// data 和 error 都是类型安全的，来自 OpenAPI Spec
```

### 6.3 状态管理约定

```typescript
// ✅ 服务端数据（API 响应）用 TanStack Query
const { data: models, isLoading } = useQuery({
    queryKey: ['models', { limit: 20 }],
    queryFn: () => api.GET('/models', { params: { query: { limit: 20 } } })
        .then(({ data }) => data),
    staleTime: 30_000,   // 30s 内不重新请求
})

// ✅ 客户端 UI 状态（弹窗、选中态）用 Zustand
const useUIStore = create<UIState>((set) => ({
    selectedModelId: null,
    setSelectedModel: (id) => set({ selectedModelId: id }),
}))

// ❌ 不要在组件内手写 fetch 调用（用 openapi-fetch）
// ❌ 不要在 TanStack Query 外管理 API 数据
```

---

## 七、安全实现约定

### 7.1 密钥不走环境变量

模型解密密钥在 Init Container 中必须通过 **Secret volume mount**（文件）读取，禁止环境变量：

```yaml
# ✅ 正确：Secret 以文件形式挂载
volumes:
  - name: decrypt-key
    secret:
      secretName: model-key-qwen
initContainers:
  - name: model-loader
    volumeMounts:
      - name: decrypt-key
        mountPath: /run/secrets/decrypt-key
        readOnly: true
```

```go
// ✅ 正确：从文件读取密钥，读完后清零内存
keyBytes, err := os.ReadFile("/run/secrets/decrypt-key/password")
defer func() { cryptobuf.Clear(keyBytes) }()
```

### 7.2 日志中不打印任何密钥相关字段

```go
// ❌ 永远禁止
slog.Warn("decrypt failed", "key", string(keyBytes))
slog.Info("api key created", "key_value", keyValue)

// ✅ 正确：只打印密钥的元信息
slog.Warn("decrypt failed", "algo", algo, "model_id", modelID)
slog.Info("api key created", "key_prefix", keyPrefix, "key_id", keyID)
```
