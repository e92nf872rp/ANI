# ANI-GW-1 — Session Gateway gRPC 接入与 Session Issuer seam 拆分

> 状态：`LOCAL_VERIFIED`
> 日期：2026-09-02
> 范围：仅 ANI Core Gateway 的 instance exec/console Session 签发；不含 Console、部署、切流、真实 Session Gateway 进程或集群 live。

## 1. 固定输入

- ANI baseline：`963bc88836c54a1b09cf100b37eb2f2cb2a5a4be`
- Session Gateway remote：`https://github.com/zhangzhe-ctrl/ani-session-gateway.git`
- Session API module：`github.com/zhangzhe-ctrl/ani-session-gateway/api`
- Session API version：`v0.1.0`
- Session API Git tag：`api/v0.1.0`
- `SESSION_GATEWAY_REPO_COMMIT`：`d86a40d33369b128aabc680d4ea0b3f790ac0bb6`

前置检查精确命中固定 ANI baseline；tag、remote、API submodule 均可解析。外部合同只从上述 commit 的 Git 对象读取 v1.2 design、SG-P0-LOCAL、status、Proto/generated client 与 WebSocket protocol；未读取工作树作为权威，也未使用历史 v1.1/resume/plan。

## 2. 结论与实现边界

ANI Gateway 的既有 exec/console REST path 与成功响应字段保持不变；real provider 不再由 Prometheus adapter 合成 URL，而是通过独立 `InstanceSessionIssuer` 调用固定 API submodule 的 `SessionService.CreateSession`。`InstanceObservability` 只保留 logs/events/metrics/security-events；Local adapter 可同时实现两个 seam，Prometheus adapter 不再签发 Session。

请求映射只包含认证上下文中的 tenant/subject、真实 instance ID、`record.Name`、typed workload kind、request ID、幂等键和 exec/VM console options；没有传 namespace、Pod/VMI URL 或用户 credential。gRPC connection 纳入 Gateway 进程关闭生命周期，单次调用默认 5 秒且可用 `SESSION_GATEWAY_GRPC_TIMEOUT` 配置。

real provider 在地址缺失/非法、连接失败、deadline 或依赖不可用时返回稳定 `ErrorResponse` 的 503，并且不生成假 URL、不回退 Local issuer。非法地址在启动时记录脱敏 warning 并保留 nil issuer，使路由 fail closed，而不是发布一个不可用的假会话地址。

OpenAPI 仅做 additive 变更：console request 增加可选 `idempotency_key`；exec/console 增加 409/422/429/500/503 声明。旧 console 客户端省略幂等键时，Gateway 只为该次 HTTP 请求生成一次 UUID，不承诺跨 HTTP 重试重放，也不复用 request ID。

## 3. Git 状态

开始前 `git status --short`：

```text
?? docs/superpowers/specs/2026-09-02-vm-ssh-key-injection-nocloud-design.md
?? repo/services/tasks/modules/plan/plan-repo-stabilization-v1.md
```

两项均为用户开始前已有文件，本批未读取、未修改、未删除。

结束时 `git status --short`（ANI-GW-1 文件按状态分组完整列示）：

```text
 M ANI-06-开发计划.md
 M repo/CURRENT-SPRINT.md
 M repo/api/openapi/v1.yaml
 M repo/deploy/real-k8s-lab/sprint13-production-shaped-gateway-deployment.yaml
 M repo/development-records/README.md
 M repo/docs/api/core.html
 M repo/pkg/adapters/runtime/local_instance_observability_service.go
 M repo/pkg/adapters/runtime/prometheus_instance_observability.go
 M repo/pkg/adapters/runtime/prometheus_instance_observability_test.go
 M repo/pkg/bootstrap/deps.go
 M repo/pkg/bootstrap/deps_test.go
 M repo/pkg/bootstrap/server.go
 M repo/pkg/ports/instance_observability.go
 M repo/scripts/validate_sprint13_b_track_production_shape.py
 M repo/services/ani-gateway/go.mod
 M repo/services/ani-gateway/go.sum
 M repo/services/ani-gateway/instance_observability_runtime.go
 M repo/services/ani-gateway/instance_observability_runtime_test.go
 M repo/services/ani-gateway/internal/router/instances.go
 M repo/services/ani-gateway/internal/router/instances_test.go
 M repo/services/ani-gateway/internal/router/router.go
 M repo/services/ani-gateway/internal/router/task_resources_test.go
 M repo/services/ani-gateway/main.go
?? docs/superpowers/specs/2026-09-02-vm-ssh-key-injection-nocloud-design.md
?? repo/development-records/ANI-GW-1.md
?? repo/pkg/ports/instance_session.go
?? repo/services/ani-gateway/instance_session_runtime.go
?? repo/services/ani-gateway/instance_session_runtime_test.go
?? repo/services/ani-gateway/internal/router/instance_session_handlers_test.go
?? repo/services/ani-gateway/internal/router/session_grpc_client.go
?? repo/services/ani-gateway/internal/router/session_grpc_client_test.go
?? repo/services/tasks/modules/plan/plan-repo-stabilization-v1.md
```

## 4. 实际修改文件

- 契约/生成物：`api/openapi/v1.yaml`、`docs/api/core.html`；PR rebase 后按当前 `main` 重生成 `services/ani-gateway/internal/authz/zz_generated_core_policies.go`。
- ports/adapters：`pkg/ports/instance_observability.go`、`pkg/ports/instance_session.go`、Local/Prometheus instance observability adapter 及其最小相关测试。
- 装配/配置：`pkg/bootstrap/{deps.go,deps_test.go,server.go}`、`services/ani-gateway/instance_observability_runtime*.go`、`instance_session_runtime*.go`、`main.go`、production-shaped Deployment 与对应 validator。
- handler/client：`services/ani-gateway/internal/router/{instances.go,instances_test.go,router.go,task_resources_test.go,instance_session_handlers_test.go,session_grpc_client.go,session_grpc_client_test.go}`。
- 依赖：`services/ani-gateway/go.mod`、`go.sum`。
- 记录/状态：本文件、`development-records/README.md`、`CURRENT-SPRINT.md`、仓库根 `ANI-06-开发计划.md`。

未修改 Session Gateway 仓库、Console/frontend、其他服务实现或既有 REST path；未复制 Proto/generated code；未添加本机 `replace`。

## 5. 验证证据

最终通过：

```text
go test ./services/ani-gateway/internal/router ./services/ani-gateway -run 'TestSessionGateway|TestInstanceSession|TestExecPreconditions|TestConsolePreconditions|TestConsoleStopped|TestGatewayInstanceSession' -count=1
go test ./pkg/ports ./pkg/adapters/runtime ./pkg/bootstrap ./services/ani-gateway/internal/router ./services/ani-gateway -count=1
go test -race ./pkg/adapters/runtime ./services/ani-gateway/internal/router ./services/ani-gateway -run 'Session|InstanceSession|ExecPreconditions|ConsolePreconditions|ConsoleStopped|GatewayInstanceSession' -count=1
go vet ./pkg/ports ./pkg/adapters/runtime ./pkg/bootstrap ./services/ani-gateway/internal/router ./services/ani-gateway
make gen-core-sdk gen-api-docs gen-gateway-authz
make validate-openapi-spec validate-core-api-compatibility validate-gateway-authz
make validate-sdk-alpha validate-sdk-beta validate-doc-api
make validate-sprint13-b-track-production-shape
make validate-architecture
make validate-doc-entrypoints
make test
python scripts/validate_yaml.py api/openapi/v1.yaml
git diff --check
```

`make test` 的 Go/Python 全仓默认测试通过。生成器复跑后 authz 无 drift，Core API compatibility 通过。第一次 OpenAPI validator 因系统 Python 缺 `openapi_spec_validator` 失败，改用按 `ci/requirements-contract.txt` 建立的临时 venv 后通过；第一次相关包/race 链接因 `/tmp` 配额失败，改用工作区临时编译目录后通过。这两项属于执行环境修正，不是代码失败。

模块图检查确认本批只新增 `github.com/zhangzhe-ctrl/ani-session-gateway/api v0.1.0`（以及其要求的 protobuf patch 升级），没有新增 client-go、KubeVirt、Redis、chi 或 WebSocket 实现依赖。图中既有 `github.com/redis/go-redis/v9` 是 baseline 已有依赖，不是本批引入。

## 6. fake/bufconn 覆盖与 `not_verified`

本地 contract integration 使用 in-process generated gRPC client/server + `bufconn`，覆盖请求字段、全部 typed workload kind、exec/serial/VNC options、request/idempotency key、响应映射和 connection close；注入 recording fake 覆盖认证上下文、denied-never-calls、全部前置校验、console 兼容 UUID、错误信封、real-provider 无 issuer 503/no fake URL。Local adapter 回归测试确认 `dev_profile.real_provider=false`。

以下明确为 `not_verified`：

- 真实 `ani-session-gateway` 进程的 CreateSession E2E。
- 真实 WebSocket/terminal/serial/VNC 数据面连接。
- 集群内 DNS、NetworkPolicy、TLS/凭据、Session Gateway Deployment/Service 与容量行为。
- production-shaped 配置的真实 rollout、切流、升级和回滚。

这些 live 项没有伪造为 passed，ANI-GW-1 的结论仅为 `LOCAL_VERIFIED`。

## 7. 回滚与发布声明

提交前回滚：从 `git diff` 精确定位上述 ANI-GW-1 文件并逐文件反向应用本批 patch；不得对整个工作树执行 reset/checkout/stash，以免覆盖用户原有未跟踪文件。若后续经人工审查形成独立 commit，则用该 commit 的常规 revert 回滚，并重新运行契约、生成和全仓测试门禁。

本批未执行 `git add`、commit、remote 修改、push、tag、SSH、部署、rollout、切流或任何集群 mutation；未开始 CONSOLE-1、LIVE-1 或其他 Work Package。

## 8. PR 准备补充（2026-09-03）

- 用户随后明确授权创建 PR；分支 `codex/ani-gw-1-session-gateway` rebase 到目标仓库 `origin/main@91b986e8c379460510aa193289cde72f827f4c91`。
- 唯一文本冲突位于 `pkg/ports/instance_observability.go`：保留上游 `InstanceLogStreamRequest/StreamLogs`，同时继续从 `InstanceObservability` 移除 exec/console Session 签发；未改变两边产品语义。
- 自动合并一度漏掉上游 `StreamLogs` 所需的 `RWMutex`；恢复 `SetLogStore/ListLogs/StreamLogs` 一致加锁后，相关包测试与 `make test` 通过。
- 当前 main 的 tenant-admin OpenAPI 变更未同步 authz registry；按现有 generator 重生成 `zz_generated_core_policies.go` 后，Gateway authz drift 与 route coverage（297/236/0）通过。该生成 diff 是 rebase 到当前 main 的门禁修复，不是 Session 业务扩面。
- `make validate-core-api-compatibility` 在 rebased branch 报既存基线不一致：当前 main 已缺少受保护路径 `/admin/tenants/{tenant_id}/transfer-ownership`，而 `api/core-v1-compatibility-baseline.yaml` 仍要求该路径；ANI-GW-1 相对 `origin/main` 的 OpenAPI diff 只包含 exec/console additive 变化，未删除该路径。本 PR 不越界修复该独立问题，并在 PR 中显式披露。
- `make validate-sprint13-b-track-production-shape` 在 rebased branch 还暴露当前 main 的另一项既存漂移：validator 要求 Gateway Dockerfile 包含 `vcluster-linux-amd64`，而当前 main 已改为 `COPY ani-tools/vcluster /usr/local/bin/vcluster`；ANI-GW-1 相对 `origin/main` 不修改该 Dockerfile，也不越界调整这项独立契约。其余 OpenAPI、Gateway authz、SDK、API docs、architecture、doc-entrypoints、相关包、race、vet 与全仓 `make test` 均通过。
