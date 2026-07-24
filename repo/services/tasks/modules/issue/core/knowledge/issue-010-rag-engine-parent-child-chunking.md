# rag-engine 父子分块

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-012, chunking portion)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§2, §5)

## Description
作为 AI 层开发者，我需要实现父子分块。子块 256-512 tokens（SentenceSplitter）+ 父块 2048 tokens（固定窗套叠）。

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/app/services/chunk_service.py`, `app/repositories/chunks.py` only

## Acceptance Criteria
- [ ] [SPEC] `chunk_service` 用 `SentenceSplitter` 切子块 256-512 tokens（优先句子边界，单句超 chunk_size 强制截断，SPEC §5.1 chunk_service）
- [ ] [SPEC] 连续子块累积到 2048 tokens 归为一个父块（固定窗套叠，SPEC §5.1）
- [ ] [SPEC] 图片链接/表格/代码块/超链接作为不可切断单元（SPEC §5.1）
- [ ] 子块 `parent_chunk_id` 指向父块，父块完整文本存入子块 `parent_content`
- [ ] 写入 `kb_chunks` 表，元数据继承（doc_id/kb_id/tenant_id/file_name/page_number/content_type）
- [ ] `make test` 通过

## Dependencies
#9 (parse service) — per SPEC §10.2 (US-012 depends on US-011).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-rag-engine §2.2, §5.1 (chunk_service)
