# Codex Handoff Record

Date: 2026-05-11

Current branch:
- `main`

Current commit:
- `70c6276 Initial ANI platform scaffold`

Remote repository:
- `https://github.com/e92nf872rp/ANI.git`

Version status:
- Current line: `v0.x` development.
- First formal release target: `v1.0.0` on 2026-09-30.
- Versioning policy: `ANI-12-版本管理策略.md`.

## Current State

The repository has been initialized, committed, and pushed to GitHub.

Implemented scope is recorded in:
- `development-records/m2-1-task-a-b-task-service-outbox.md`

Implemented stages:
- `M2.1-TASK-A`: minimal `task-service` query interface.
- `M2.1-TASK-B`: transactional outbox publisher loop.

Naming clarification:
- Current work belongs to `ANI-06 / 模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架`.
- It does not belong to `ANI-06 / 模块 3：模型管理平台`.
- Earlier records used `Stage 3A/3B/3C` as internal code-generation slice names. Going forward, use `M2.1-TASK-A/B/C`.

Additional setup completed:
- Go toolchain is installed locally.
- `buf` is installed locally.
- `Makefile` includes `tools-proto`, so protobuf generation can bootstrap required Go protoc plugins in a fresh environment.
- Root `.gitignore` excludes local caches, build outputs, temporary files, `.env`, and local generated tool binaries.

## Validation Completed

The following commands passed on 2026-05-11:

```bash
cd repo
make gen-proto
make test
make build
```

Generated local artifacts are intentionally ignored:
- `repo/.bin/`
- `repo/.cache/`
- `repo/bin/`
- Python `__pycache__/`

## Security And GitHub Notes

GitHub remote is configured with a credential-free HTTPS URL:

```bash
https://github.com/e92nf872rp/ANI.git
```

Do not store GitHub tokens in:
- Git remote URLs.
- Prompt text.
- Repository files.
- `.env.example`.
- Commit messages.

Use GitHub OAuth, Git Credential Manager, or a local one-time credential prompt for authentication.

## Next Development Stage

Next implementation slice should be `M2.1-TASK-C` only:

Task Service Worker Mutation RPCs with tenant-safe writes.

Do not start the following until `M2.1-TASK-C` is accepted:
- Frontend feature work.
- MinIO object storage workflow.
- Inference operator implementation.
- Billing/metering workflow expansion.
- Module 3 model management platform work.
- Broad API cleanup unrelated to task worker mutation safety.

## M2.1-TASK-C Scope

Required work:
- Review `ANI-08-安全架构设计.md`, `ANI-09-数据模型设计.md`, `ANI-11-代码实现规范.md`, and the existing task service code.
- Fix `api/proto/task/v1/task_service.proto` so worker mutation RPCs have enough tenant/security context for safe RLS-backed writes.
- Regenerate protobuf code with `make gen-proto`.
- Implement tenant-safe worker mutation RPCs in `services/task-service`.
- Preserve transactional outbox guarantees.
- Add focused tests for repository/service behavior where practical.
- Update a new development record:
  - `development-records/m2-1-task-c-worker-mutations.md`

Required validation:

```bash
cd repo
make gen-proto
make test
make build
```

## Suggested Codex Cloud Prompt

```text
请加载本仓库，先阅读 CLAUDE.md、ANI-00 到 ANI-12 设计文档，以及 repo/development-records/README.md、repo/development-records/m2-1-task-a-b-task-service-outbox.md 和 repo/development-records/2026-05-11-handoff-codex-cloud.md。

继续 `M2.1-TASK-C` 开发，只做 `ANI-06 / 模块 2 / 2.1 Gateway 骨架 / NATS JetStream 异步任务框架` 下的 Task Service Worker Mutation RPC 与多租户安全闭环。

注意：不要进入 `ANI-06 / 模块 3：模型管理平台`。历史记录里的 `Stage 3A/3B/3C` 是旧的内部代码生成切片名，不代表模块 3。

要求：
1. 基于设计文档，不绕过 PostgreSQL RLS 和 tenant_id 安全边界。
2. 先修正 repo/api/proto/task/v1/task_service.proto 中 worker mutation RPC 缺少安全租户上下文的问题。
3. 执行 make gen-proto，重新生成 protobuf 代码。
4. 实现 task-service 中安全的 worker mutation RPC：领取任务、更新状态、记录错误、完成任务等。
5. 所有写操作必须有明确 tenant_id / worker 身份 / task ownership 校验。
6. 保持 outbox 事务一致性，不引入双写风险。
7. 补充必要单元测试或仓储层测试。
8. 在开发记录中标注版本影响：如果修改 task_service.proto，则按 ANI-12 判定是 MINOR、PATCH 还是 no-release-impact；不得创建正式版本 tag。
9. 执行并通过：
   - make gen-proto
   - make test
   - make build
10. 新增开发记录：repo/development-records/m2-1-task-c-worker-mutations.md，记录：
   - 实现了哪些功能
   - 修改了哪些文件
   - 做了哪些测试
   - 哪些设计风险仍然保留
   - 版本影响判定
11. 使用独立分支：codex/m2-1-task-c-worker-mutations，完成后提交 Pull Request，不要直接覆盖 main。
```
