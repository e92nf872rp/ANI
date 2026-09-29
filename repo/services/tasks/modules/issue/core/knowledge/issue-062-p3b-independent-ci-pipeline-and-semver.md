# KB-SPLIT-P3B CI / 镜像流水线独立：迁出 monorepo + SemVer tag

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 基础设施负责人，我需要把 kb 的构建与 CI 门禁从 monorepo 统一入口迁出为独立流水线：镜像独立构建推送、独立 CI（lint/test/architecture 校验按 kb 仓库自带 make 目标）、镜像独立 tag 采用 SemVer，实现"独立仓库、独立构建、独立版本"三独立中的构建与版本独立。

## Scope
- Product line: core (kb-service / CI infra)
- Code paths allowed: `repo/services/kb-service/`（Makefile 自包含、CI 配置、Dockerfile）、CI 平台配置（kb 独立 pipeline 定义）
- monorepo 统一入口中 kb 相关条目摘除（在 #068 仓库拆分时彻底收尾）

## Acceptance Criteria
- [ ] [ADR §四 #9] kb CI/构建流水线独立：不再挂 monorepo 统一入口；独立 pipeline 配置落位（lint / test / migration 检查 / 镜像构建）
- [ ] [ADR §三-1] 镜像独立 tag：SemVer（MAJOR.MINOR.PATCH），与"独立版本"目标对齐；tag 规则写入 docs
- [ ] [ADR §五-阶段3] Dockerfile 自包含：Python 3.11 基础镜像、依赖钉死安装、双栈单进程入口（uvicorn :8002 + gRPC :50053）保持 main.py 启动结构
- [ ] kb 仓库 Makefile 自带 `make test` / `make validate-architecture` 等门禁目标（不依赖 monorepo 根 Makefile）
- [ ] 流水线全绿验证：一次完整 lint + test + 构建产出镜像
- [ ] `git diff --check` 通过（kb 仓库内）
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#049（基线固定；与阶段 1/2 并行，ADR §六）

## Type
infra (feature)

## Priority
medium

## Labels
core, kb-service, ci, docker, semver, split, adr-0001

## Batch
KB-SPLIT-P3B

## References
- ADR: §四 #9（monorepo 构建耦合点）、§三-1（独立构建/独立版本）、§五-阶段3、§六（阶段 2/3 与阶段 1 并行）
