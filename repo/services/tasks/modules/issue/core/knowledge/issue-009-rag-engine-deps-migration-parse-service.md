# rag-engine 依赖迁移与解析服务

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-011)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§2, §5)

## Description
作为 AI 层开发者，我需要迁移 rag-engine 依赖到 LlamaIndex 并实现文档解析服务。

## Scope
- Product line: core (Services / rag-engine)
- Code paths allowed: `repo/ai/rag-engine/requirements.txt`, `app/services/parse_service.py`, `app/clients/ocr_api.py` only

## Acceptance Criteria
- [ ] [SPEC] 移除 pymilvus/langchain 旧依赖，改为 `llama-index-core` + `readers-docling` + `embeddings-huggingface` + `llms-openai-like` + `vector-stores-milvus` + `pymilvus`（SPEC §1.3）
- [ ] [SPEC] `parse_service` 用 DoclingReader 解析 PDF/Word/Excel/PPT/MD/TXT（按 plan.md §4.1 规则，SPEC §5.1 parse_service 算法）
- [ ] `parse_service` 对扫描页（`page.extract_text()` < 50 字符）调 AI 服务 PaddleOCR API
- [ ] [SPEC] 表格转 HTML，跨页表格按页拆分，表格 > 2048 tokens 按行分组拆分并保留表头（SPEC §5.1）
- [ ] [SPEC] 图片提取上传 MinIO 获取 URL，插入 `[图片: caption](OSS_URL)` 占位节点（SPEC §5.1）
- [ ] `make test` 通过

## Dependencies
#5 (OCR API) — per SPEC §10.2 (US-011 depends on US-006).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-B

## References
- SPEC: spec-services-rag-engine §1.3, §2.2, §5.1
