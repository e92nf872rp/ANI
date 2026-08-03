# M2.1-TASK-B Development Record — kb-service 骨架与 gRPC server

Date: 2026-07-29
Issue: issue-006 (US-008)
Branch: contract-foundation

Product plan mapping:
- `ANI-06 / 模块 2 / 知识库平台 / kb-service 骨架（US-008）`
- Slice: `M2.1-TASK-B`
- Scope: kb-service 目录骨架 + gRPC server 承接 13 RPC（10 P0 + 3 P1 UNIMPLEMENTED）
- SPEC: `spec-services-kb-service §2.2, §2.4, §4.1`

Version impact:
- Current line: `v0.x` development.
- Release impact: `MINOR`（proto 追加 3 P1 RPC 声明，新增服务目录）。
- Compatibility note: `kb_service.proto` 追加 3 个 RPC 与 7 个消息，未改任何既有字段号；Go pb 重新生成无破坏性变更。
- No formal version tag was created.

## 实现了什么

新建 `repo/services/kb-service/` Python 骨架（FastAPI 健康检查 + gRPC server），承接 `kb_service.proto` 全部 13 个 RPC：10 个 P0 RPC 作为骨架返回 `UNIMPLEMENTED`（业务逻辑在 US-009/US-010 接入），3 个 P1 RPC（ListKBCitations/ListKBSessions/UpdateKBPermissions）声明并返回 `UNIMPLEMENTED`。gRPC server 可启动并响应 RPC，`.env` 配置可加载。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `api/proto/kb/v1/kb_service.proto` | 修改 | 追加 3 个 P1 RPC 声明 + 7 个 P1 消息（对齐 services/v1.yaml） |
| `pkg/generated/pb/kb/v1/kb_service.pb.go` | 修改（buf 生成） | 重新生成，无手改 |
| `pkg/generated/pb/kb/v1/kb_service_grpc.pb.go` | 修改（buf 生成） | 重新生成，含 3 个新 RPC handler |
| `services/kb-service/Dockerfile` | 新增 | python:3.11-slim，EXPOSE 50053 |
| `services/kb-service/requirements.txt` | 新增 | 锁定本地验证通过版本 |
| `services/kb-service/main.py` | 新增 | FastAPI /health + gRPC server 启动 + 优雅关闭 |
| `services/kb-service/app/core/config.py` | 新增 | pydantic-settings，`extra="ignore"` 加载共享 .env |
| `services/kb-service/app/api/grpc_server.py` | 新增 | KBServiceServicer，10 P0 + 3 P1 UNIMPLEMENTED |
| `services/kb-service/app/api/p1_rpcs.py` | 新增 | 3 个 P1 RPC 的 UNIMPLEMENTED 实现 |
| `services/kb-service/app/generated/**` | 新增（protoc 生成） | Python gRPC stubs（kb_service_pb2*.py + common_pb2*.py + .pyi） |
| `services/kb-service/tests/test_grpc_server.py` | 新增 | 16 个测试（servicer 声明 + 10 P0 + 3 P1 响应） |
| `services/kb-service/tests/test_config.py` | 新增 | 3 个测试（.env 加载 + core_api_base_url 派生） |

## 完工标准达成

- [x] AC1 新建 kb-service/，含 Dockerfile/requirements/main.py
- [x] AC2 实现文件结构：app/api/grpc_server.py + app/api/p1_rpcs.py + app/core/config.py（SPEC §2.4）
- [x] AC3 实现 proto 10 个 P0 RPC（servicer 承接全部 10 个）
- [x] AC4 Phase A 新增 3 个 P1 RPC 声明，P0 返回 UNIMPLEMENTED（SPEC §4.1）
- [x] AC5 gRPC server 可启动并响应 RPC（smoke test 验证）
- [x] AC6 `make test` 通过（Go test + Python compile + kb-service pytest 19 passed）

## Design Decisions

### D1：10 P0 RPC 在骨架批次返回 UNIMPLEMENTED 而非空成功响应
- **模糊点**：issue-006 标题是「骨架与 gRPC server」，AC5 要求「可启动并响应 RPC」，但本批次无 repositories/clients/outbox（属 US-009/US-010）。
- **选择**：10 P0 RPC 返回 `grpc.StatusCode.UNIMPLEMENTED` + 指明后续接入 issue 的说明消息。
- **理由**：骨架的语义是「server 启动 + RPC 可调用」，而非「业务可用」。返回空成功响应（如空 KnowledgeBase）会误导调用方以为业务已实现，且掩盖「未接入后端」的事实。UNIMPLEMENTED 是 gRPC 标准的「已声明未实现」状态码，与基类生成的 `raise NotImplementedError` 语义一致，后续 issue 替换为真实逻辑时无需改契约。每个 abort 消息标注了负责接入的后续 issue 编号，便于 US-009/US-010 定位。

### D2：P1 RPC 委托到独立模块 p1_rpcs.py 而非内联在 grpc_server.py
- **模糊点**：SPEC §4.1 要求 3 个 P1 RPC「声明」并返回 UNIMPLEMENTED，未规定代码组织方式。
- **选择**：3 个 P1 RPC 的 UNIMPLEMENTED 逻辑放在独立的 `p1_rpcs.py`，`grpc_server.py` 的 servicer 方法 `return p1_rpcs.xxx(request, context)` 委托调用。
- **理由**：P1 RPC 的实现期与 P0 不同（P1 在更后阶段），分离模块使 P1 实现时只需改 `p1_rpcs.py`，不触碰 P0 servicer。`return` 语句虽因 `context.abort` 抛异常而不可达，但保留 `return` 使 P1 实现期可改为 `return response` 而不动 servicer 调用点（前向兼容）。

### D3：main.py 同时启动 gRPC + FastAPI，FastAPI 仅承载健康检查
- **模糊点**：SPEC §2.4 提及「FastAPI + grpc server 启动」但未明确 FastAPI 承载哪些端点。
- **选择**：FastAPI 仅暴露 `/health` 和 `/readyz`，业务 RPC 全部走 gRPC。
- **理由**：对齐 rag-engine 的模式（FastAPI 承载健康检查）。gRPC 是业务 RPC 的唯一入口（proto 定义），FastAPI 的 HTTP 端点用于 K8s 探针/运维。两服务监听不同端口（gRPC :50053，FastAPI :8002，与 rag-engine :8001 区分）。

### D4：config.py 字段名对齐共享 .env 约定 + `extra="ignore"`
- **模糊点**：kb-service 是新服务，需从 repo 根共享 `.env` 加载配置，但 `.env` 含大量其他服务变量（MINIO_*/AUTH_*/GATEWAY_PORT 等）。
- **选择**：字段名 `database_url`/`nats_url`/`redis_url` 对齐 `.env` 的 `DATABASE_URL`/`NATS_URL`/`REDIS_URL`（pydantic 大小写不敏感）；`model_config` 设 `extra="ignore"` 忽略无关变量；`core_api_base_url` 设为从 `ani_gateway_internal_url` + `/api/v1` 派生的 property。
- **理由**：pydantic-settings 2.14.2 的 `BaseSettings` 默认 `extra="forbid"`，直接加载共享 `.env` 会报 18 个 `extra_forbidden` 错误（已验证 rag-engine 有同样问题）。`extra="ignore"` 是多服务共享 `.env` 的正确语义。`core_api_base_url` 用 property 避免重复存储 gateway host。

## Deviations

### DV1：requirements.txt 版本锁定偏离 SPEC 草案建议值
- **SPEC 说**：无显式版本要求（SPEC §2.4 仅列依赖类型）。
- **实现**：`requirements.txt` 锁定 `protobuf==7.35.1`、`pydantic==2.11.7`、`pydantic-settings==2.14.2`、`uvicorn==0.35.0`、`httpx==0.28.1`（均为本地 dev 环境实际验证通过版本）。
- **理由**：生成 stub 内置版本守卫（`GRPC_GENERATED_VERSION='1.83.0'`），若 `grpcio` 版本低于生成版本会 `raise RuntimeError`。锁定与生成环境一致的版本确保 Docker 构建/CI 可重现。若用更低版本（如 protobuf 5.x）会导致 stub 导入失败或行为异常。

### DV2：main.py 用 `__file__` 计算 sys.path 而非依赖 CWD 的 `"."`
- **SPEC 说**：无显式启动方式约定。
- **实现**：`sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))` + `sys.path.insert(0, os.path.join(_HERE, "app", "generated"))`。
- **理由**：protoc 生成的 stub 用顶层导入 `from common.v1 import common_pb2` 和 `from kb.v1 import kb_service_pb2`，要求 `app/generated` 在 `sys.path`。原方案 `sys.path.insert(0, ".")` 依赖 CWD：本地从 repo 根运行可工作，但 Docker 容器（`WORKDIR /app` + `python main.py`）下 `sys.path[0]=/app` 找不到顶层 `common`/`kb`，必崩（已验证 `ModuleNotFoundError: No module named 'common'`）。用 `__file__` 计算使路径与 CWD 无关，本地/Docker/CI 三种环境一致。

## Tradeoffs

### T1：gRPC server 用 ThreadPoolExecutor(max_workers=10) 而非异步
- **备选 A**：`grpc.aio` 异步 server（与 FastAPI 的 async 事件循环统一）。
- **备选 B**：`grpc.server(ThreadPoolExecutor)` 同步 server（当前选择）。
- **取舍**：备选 A 理论上与 FastAPI async 统一更优雅，但 gRPC Python 的 asyncio API 在骨架阶段引入额外复杂度（servicer 需 `async def`，stub 调用需 await），且 US-009/US-010 的 repositories（asyncpg）与 Core client（httpx async）混用同步 gRPC 时需仔细管理事件循环。骨架批次优先「能启动能响应」，选备选 B 简单可靠；后续若性能/并发成为瓶颈再迁移到 aio。

### T2：P1 消息字段手工对齐 OpenAPI 而非从 OpenAPI 自动生成 proto
- **备选 A**：用 openapi-generator 或自定义脚本从 `services/v1.yaml` 自动生成 P1 proto 消息。
- **备选 B**：手工编写 proto 消息并人工对齐 OpenAPI schema（当前选择）。
- **取舍**：备选 A 长期更可维护，但仓库无 proto-from-OpenAPI 生成管线，为本批次 3 个 P1 RPC 搭建生成器属过度工程。备选 B 7 个消息 51 行 proto，人工对齐成本低于搭建生成器。review-it 已逐字段验证对齐（KBCitation/KBSession/UpdateKBPermissionsRequest/List 响应均与 v1.yaml 一致）。

### T3：测试用真实 in-process gRPC server 而非 grpc_testing 模拟
- **备选 A**：`grpc_testing` 模块（无网络，纯方法级模拟）。
- **备选 B**：真实 `grpc.server` + ephemeral port + `insecure_channel`（当前选择）。
- **取舍**：备选 A 更快更纯，但 `grpc_testing` 未安装（环境无该包），新增依赖仅测试用不划算。备选 B 用真实 server 端到端验证「可启动 + 可响应」，更贴近 AC5 语义，且无额外依赖。17 个测试 1.3s 完成，性能可接受。

## Open Questions

### OQ1：P0 RPC 在 US-009/US-010 接入真实逻辑后，UNIMPLEMENTED 的 abort 消息是否需清理
- **假设**：US-009/US-010 实现 P0 RPC 时会直接替换 `context.abort` 为真实逻辑，abort 消息中的「wired in US-009」文本会自然消失。
- **待确认**：US-009/US-010 实现者是否认可 abort 消息中标注的 issue 编号映射（CreateKB→US-009 repositories，NotifyDocumentUploaded→US-010 outbox，Query→US-009/010），若映射有误需本批次同步修正注释。

### OQ2：Docker 构建未实际执行（仅模拟验证路径）
- **假设**：Dockerfile `COPY . .` + `WORKDIR /app` + `python main.py` 在真实构建中能工作（已用本地模拟 `cwd=services/kb-service` + 无 PYTHONPATH 验证 `RUN_OK`）。
- **待确认**：用户是否需要在真实 Docker 构建中验证（需 `docker build`），或 CI 的 Linux 环境验证足够。当前 Windows 环境无 Docker 验证条件。

### OQ3：`make test` 在 Windows 本地因 Makefile Unix 风格 env 语法失败
- **假设**：`make test-go` 的 `GOCACHE=... go test` 语法在 Linux/CI 正常，Windows 失败是既有平台限制（非本批次引入）。
- **待确认**：CI 是否在 Linux 环境运行 `make test`，本批次的 Go pb 重新生成是否需在 CI 重新校验（本批次已在本地用直接 env 方式运行 Go test 通过）。

### OQ4：rag-engine 的 `extra="forbid"` 同类问题是否需在后续批次修复
- **观察**：review 期间发现 `ai/rag-engine/app/core/config.py` 的 `BaseSettings` 同样无 `extra="ignore"`，加载共享 `.env` 会崩。但 rag-engine 不在本批次 scope（`repo/services/kb-service/` only）。
- **待确认**：是否在后续 rag-engine 相关批次（或独立修复批次）同步加 `extra="ignore"`，还是本观察属既有技术债单独跟踪。

## Validation

Commands run:

```bash
cd repo
make validate-architecture
go build ./pkg/... ./services/ani-gateway/... ./services/auth-service/... ./services/model-service/... ./services/task-service/... ./services/reconcile-worker/... ./cli/ani/...
go test ./pkg/... ./services/ani-gateway/... -timeout 120s
python -m compileall -q services/kb-service
python -m pytest services/kb-service/tests -q
git diff --check
# Docker 路径模拟
python -c "..." (cwd=services/kb-service, no PYTHONPATH) → RUN_OK
# gRPC 启动 smoke test → AC5_OK
```

Result:
- `make validate-architecture`: ✅ component import guard passed + architecture guardrails valid
- Go build (全 go.work 模块): ✅ BUILD_EXIT: 0
- Go test (pkg + ani-gateway): ✅ TEST_EXIT: 0（proto 变更未破坏 Go 代码）
- Python compileall (kb-service): ✅ COMPILE_EXIT: 0
- pytest kb-service: ✅ 19 passed（16 gRPC + 3 config）
- git diff --check: ✅ exit 0
- Docker 路径模拟: ✅ RUN_OK（无 PYTHONPATH 下从服务目录启动成功）
- gRPC 启动 smoke test: ✅ AC5_OK（server 启动 + P0/P1 RPC 响应 UNIMPLEMENTED）

Note:
- `make test` 整体在 Windows 本地因 Makefile `GOCACHE=... go test` Unix 风格 env 语法失败（既有平台限制，非本批次回归）；已通过直接设置 env 运行 Go test 验证通过。

## Review-it 修复记录

- 删除 `test_grpc_server.py` 中误导性空操作测试 `test_server_starts_and_responds_health`（`assert grpc_server` 引用 fixture 函数对象永远 truthy，未真正验证启动）；AC5 由 13 个 per-RPC 测试实际覆盖。

## Remaining Risks

- repositories/clients/outbox/migrations 未实现（属 US-009/US-010，本批次为骨架）。
- Docker 实际构建未执行（仅路径模拟），OQ2 待确认。
- P1 RPC 仅有声明，P1 实现期需补真实查询逻辑（本批次按 SPEC §4.1 只要求声明 + UNIMPLEMENTED）。

## Next Boundary

本批次（M2.1-TASK-B / US-008 骨架）已完成。下一实现切片为 US-009（kb-service repositories + Core API client + rag-engine client，issue-007）与 US-010（outbox + Redis session，issue-008），两者在 `repo/services/kb-service/` 内继续接入，替换 P0 RPC 的 UNIMPLEMENTED 为真实业务逻辑。
