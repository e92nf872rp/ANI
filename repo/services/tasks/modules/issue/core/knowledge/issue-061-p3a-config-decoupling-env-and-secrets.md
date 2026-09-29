# KB-SPLIT-P3A 配置脱钩：自建 .env.example + embedding 键专属命名 + Secret 化

## Document Links
- Plan: `repo/services/tasks/modules/plan/knowledge_base/0001-split-kb-from-core.md`
- UX: N/A — backend-only
- SPEC: N/A

## Description
作为 kb-service 开发者，我需要把配置从 monorepo 根 `.env`（config.py `env_file=".env"` 指向 repo 根）脱钩：自建 kb 自己的 `.env.example`；`embedding_model`/`embedding_dim` 共享环境键改为 kb 专属命名或显式声明来源；敏感项（MinIO service account 凭据、Milvus 凭据、PG 凭据、NATS 等）走 K8s Secret。

## Scope
- Product line: core (kb-service)
- Code paths allowed: `repo/services/kb-service/app/core/config.py`、`repo/services/kb-service/.env.example`（新建）、`repo/services/kb-service/`（K8s 清单 Secret 引用，若适用）
- 不动 monorepo 根 `.env`（其他服务仍用）

## Acceptance Criteria
- [ ] [ADR §四 #5] config.py 不再指向 monorepo 根 `.env`；新建 kb 自有 `.env.example`（全部配置项列出、含说明、不含真实凭据）
- [ ] [ADR §一-1.4] `embedding_model`/`embedding_dim` 改 kb 专属命名（如 `KB_EMBEDDING_MODEL`/`KB_EMBEDDING_DIM`）或显式声明来源；默认值不变（BAAI/bge-m3 / 1024），与 rag-engine 能力合同对齐
- [ ] [ADR §五-阶段3-配置] 敏感项走 K8s Secret：MinIO service account 凭据、Milvus 凭据、PG 凭据、NATS 认证、Redis（如涉及）；.env.example 中对应项为占位符
- [ ] [ADR §五-阶段3-配置] 向量 collection 维度（#056）与 embedding 键重命名联动一致，无默认值漂移
- [ ] [ADR §五-阶段3] requirements 全钉死复查（Python 3.11 + FastAPI + gRPC + pymilvus 等全依赖固定版本）
- [ ] 配置加载单测：缺必填项快速失败（fail-fast）、默认值正确
- [ ] `make test` + `make validate-architecture` + `git diff --check` 通过
- [ ] progress 文档闭环 4 件套（CLAUDE.md §6-3）

## Dependencies
#056 #058（Milvus/MinIO 客户端配置项已落位，命名在其上收口）

## Type
core (feature)

## Priority
medium

## Labels
core, kb-service, config, secrets, split, adr-0001

## Batch
KB-SPLIT-P3A

## References
- ADR: §四 #5（根 .env 耦合点）、§一-1.4（embedding 共享键）、§五-阶段3（配置脱钩）、§三-1（requirements 全钉死）
