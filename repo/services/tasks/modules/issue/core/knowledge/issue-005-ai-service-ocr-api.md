# 新增 AI 服务 OCR API 端点

## Document Links
- PRD: `repo/services/tasks/modules/prd/core/knowledge/prd-core-knowledge-base-platform.md` (US-006)
- UX: N/A — backend-only
- SPEC: `repo/services/tasks/modules/spec/core/knowledge/spec-services-rag-engine.md` (§4.2, §5)

## Description
作为平台开发者，我需要 AI 服务提供 OCR 推理端点，供 rag-engine 调用 PaddleOCR 处理扫描件，而非本地安装 PaddleOCR。

## Scope
- Product line: core (Services / inference-service)
- Code paths allowed: `repo/services/inference-service/` only

## Acceptance Criteria
- [ ] 在 inference-service 新增 OCR 推理 RPC（或独立 ocr-service），后端部署 PaddleOCR（PP-OCRv4）
- [ ] OCR API 支持 `lang=ch`、`use_angle_cls=True` 参数
- [ ] OCR API 返回版面区域分类（文字/表格/图片）与表格 HTML
- [ ] OCR API 返回 `ocr_confidence` 字段
- [ ] [SPEC] 返回 `OCRResult` schema：`regions[].type ∈ {text,table,figure}` + `regions[].table_html` + `ocr_confidence`（SPEC §4.2）
- [ ] rag-engine 可通过 client 调用该 OCR API
- [ ] `make test` 通过

## Dependencies
#2 (ocr capability 标注) — per SPEC §10.2 Issue Mapping (US-006 depends on US-004).

## Type
core (services)

## Priority
high

## Labels
core, services

## Batch
M2.1-TASK-A

## References
- SPEC: spec-services-rag-engine §4.2, §5 (US-006)
