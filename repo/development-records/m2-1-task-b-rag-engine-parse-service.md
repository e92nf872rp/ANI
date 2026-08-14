# M2.1-TASK-B — rag-engine 依赖迁移与文档解析服务 (Issue #009)

完成日期：2026-07-31
对应 Sprint：Sprint 14
验证结果：53 tests passed, make validate-architecture passed

| 字段 | 值 |
|---|---|
| Issue | #009 — rag-engine 依赖迁移与解析服务 |
| PRD | US-011 (`prd-core-knowledge-base-platform.md`) |
| SPEC | `spec-services-rag-engine.md` §1.3, §2.2, §5.1 |
| Batch | M2.1-TASK-B |

## 实现了什么

迁移 rag-engine 依赖到 LlamaIndex 技术栈，实现 `parse_service` 支持 6 种格式（PDF/DOCX/XLSX/PPTX/MD/TXT），输出扁平 `list[ParsedNode]` 携带语义 metadata（sub_type/heading_level/section_path），表格转 HTML 并大表拆分，图片提取上传 MinIO。同时修复了集群 Kube-OVN 网络故障恢复 MinIO/Milvus 服务。

## 关键文件改动

| 文件 | 新增/修改 | 说明 |
|---|---|---|
| `ai/rag-engine/requirements.txt` | 修改 | 移除 langchain/paddleocr，改为 llama-index-core 等 6 个包 + minio + pymupdf |
| `ai/rag-engine/app/core/config.py` | 修改 | 新增 milvus_addr/ocr_api/minio 配置字段，pydantic v2 SettingsConfigDict |
| `ai/rag-engine/app/clients/minio_client.py` | 新增 | ImageUploader — 上传图片到 MinIO ani-kb-docs bucket |
| `ai/rag-engine/app/clients/ocr_api.py` | 新增 | OcrApiClient — 调用 inference-service PaddleOCR（本期未使用，预留） |
| `ai/rag-engine/app/services/parse_service.py` | 新增 | 核心：ParseService + _emit_text_table_nodes + PDF/Office 解析 |
| `ai/rag-engine/tests/test_parse_service.py` | 新增 | 45 个纯逻辑单测 |
| `ai/rag-engine/tests/test_e2e_parse.py` | 新增 | 8 个端到端测试（含 MinIO 真实上传） |

## 完工标准达成

- [x] 移除 pymilvus/langchain 旧依赖，改为 llama-index-core + readers-docling + embeddings-huggingface + llms-openai-like + vector-stores-milvus + pymilvus（SPEC §1.3）
- [x] parse_service 解析 PDF/Word/Excel/PPT/MD/TXT（SPEC §5.1）
- [x] 表格转 HTML，大表 > 2048 tokens 按行分组拆分保留表头（SPEC §5.1）
- [x] 图片提取上传 MinIO，插入 [图片: caption](OSS_URL) 占位节点（SPEC §5.1）
- [x] 53 tests passed, make validate-architecture passed

---

## Implementation Notes

### 1. Design Decisions

**1.1 PDF 使用 PyMuPDF 轻量解析而非 DoclingReader**

- **Ambiguity:** SPEC §5.1 伪代码说 "for page in doc.pages: text = page.extract_text()"，但未明确用哪个库。plan.md 提到 DoclingReader。
- **Choice:** PDF 路径用 PyMuPDF (fitz) `get_text("dict")` 而非 DoclingReader。
- **Rationale:** 用户明确要求 "pdf 只需读取纯文字，不需要使用模型进行读取，后续会部署 ocr 模型"。DoclingReader 处理 PDF 需要下载模型（~2GB），PyMuPDF 纯文本提取秒级完成，无需模型。扫描页 OCR 留给后续部署的 inference-service。

**1.2 MD/TXT 直接读取而非 DoclingReader**

- **Ambiguity:** SPEC §5.1 说用 DoclingReader 解析所有格式。
- **Choice:** MD/TXT 用 `Path.read_text()` 直接读取，不经过 DoclingReader。
- **Rationale:** 实测发现 DoclingReader 会把 TXT 中的 pipe 表格当作普通文本重新格式化，破坏原始 `| col | col |` 结构。直接读取保留原始 markdown，pipe 表格能被 `_split_tables_and_text` 正确识别转 HTML。

**1.3 ParsedNode 携带 metadata 上下文提示字段（而非层级结构）**

- **Ambiguity:** SPEC §5.1 的 parse_service 输出是扁平 list[ParsedNode]，但用户询问是否应升级为"带层级标签的语义流"。
- **Choice:** 保持扁平列表，在 metadata 中增加非层级性的上下文提示：`sub_type`（heading/paragraph/table/image/list）、`heading_level`（1-6）、`section_path`（面包屑 "Chapter > Section"）、`table_index`/`row_count`/`is_large_table`。
- **Rationale:** SPEC 明确把父子层级设计为 chunk_service（Issue #010）的职责。在 parse_service 加层级会违反架构分层、与 chunk_service 重复。metadata 是信息丰富化，不改变扁平输出契约，chunk_service 可利用这些提示做更精准的切分。

**1.4 PDF 标题检测用字号众数（mode）而非中位数（median）**

- **Ambiguity:** SPEC 未规定 PDF 标题检测方式。
- **Choice:** 用 `get_text("dict")` 获取每个 span 的字号，以**众数**（出现频率最高的字号）作为 body 正文基准，字号 ≥ body×2.0 → `#`，≥ body×1.5 → `##`，≥ body×1.25 → `###`。
- **Rationale:** 真实文档中 body 文本占绝大多数，众数能准确反映正文字号。中位数在测试用例（仅 4 行文本）中偏大导致阈值全部偏高、无法识别子标题。众数对小样本和大样本都稳定。

**1.5 DoclingReader 每次 parse 新建实例（不缓存单例）**

- **Ambiguity:** 性能优化时考虑缓存 DoclingReader 单例。
- **Choice:** 每次 `parse()` 调用新建 `DoclingReader()` 实例。
- **Rationale:** 实测发现 DoclingReader 内部有文档缓存，单例在不同文档间会串数据（解析 DOCX 时返回之前 TXT 的内容）。虽然新建实例有微小开销，但保证文档隔离正确性。

### 2. Deviations

**2.1 扫描页 OCR 推迟到后续阶段**

- **Spec:** AC 要求 "parse_service 对扫描页（page.extract_text() < 50 字符）调 AI 服务 PaddleOCR API"。
- **Implementation:** `ocr_api.py` 客户端已实现，但 parse_service 未集成扫描页检测和 OCR 回退逻辑。
- **Rationale:** 用户明确说 "后续会部署 ocr 模型进行这些操作，本期只需要读取 pdf 文字版，如果是图片只保存为 oss"。inference-service 的 OCR 端点尚未部署，本期聚焦文字版 PDF 解析。OcrApiClient 已就绪，后续集成只需在 `_parse_pdf_lightweight` 中加 `if len(text) < 50` 分支。

**2.2 Word/Excel/PPT 图片直接从文件提取，不走 Docling REFERENCED 模式**

- **Spec:** plan.md §4.4 设计用 Docling REFERENCED 模式导出图片到磁盘，再上传 MinIO 重写 markdown 中的图片引用。
- **Implementation:** 用 python-docx / openpyxl / python-pptx 直接从文件结构提取嵌入图片，上传 MinIO 后作为独立的 image 节点追加到 nodes 列表末尾。
- **Rationale:** Docling REFERENCED 模式需要下载模型、配置 pipeline_options、调用 `save_as_markdown`，流程复杂且 PDF 路径已改用 PyMuPDF。直接从 Office 文件结构提取图片更简单、更快、不依赖 Docling 模型。代价是图片节点位置在末尾而非内联（后续 chunk_service 可用 page_number 做近似定位）。

**2.3 requirements.txt 版本与 SPEC 不完全一致**

- **Spec:** SPEC §1.3 列出 `pymilvus==2.5.0`。
- **Implementation:** 改为 `pymilvus>=2.5.0`，让 pip 自动解析兼容版本。
- **Rationale:** `llama-index-vector-stores-milvus==1.1.0` 依赖 `pymilvus>=2.4.0,<3.0.0`，固定 2.5.0 与 llama-index 包冲突。`>=2.5.0` 让 pip 解析出同时满足两者的版本。

### 3. Tradeoffs

**3.1 PDF 表格检测：pipe 文本 vs. 结构化提取**

- **Alternative A:** 用 PyMuPDF `page.find_tables()` 结构化提取表格（准确但依赖 PDF 表格线）。
- **Alternative B:** 将 PDF 文本按行提取，用 `_split_tables_and_text` 检测 pipe 表格（简单但只识别 `|---|` 格式的文本表格）。
- **Choice:** Alternative B。
- **Pros:** 复用 MD/Office 的同一套表格处理逻辑（`_emit_text_table_nodes`），代码一致性高。
- **Cons:** 无法检测无边框表格（PDF 中用空格对齐的表格）。后续 OCR 阶段可补充结构化表格提取。

**3.2 标题检测：正则匹配 vs. Docling 语义标签**

- **Alternative A:** 用 `_HEADING_RE` 正则匹配 `#{1-6}` ATX 标题（通用，所有格式统一）。
- **Alternative B:** 用 Docling 的 `doc.items` 语义标签获取 heading level（准确但仅限 Docling 路径，PDF 不走 Docling）。
- **Choice:** Alternative A。
- **Pros:** PDF（PyMuPDF 生成的 `#` markdown）和 Office（Docling 生成的 `##` markdown）走同一套标题检测逻辑，无需分支。
- **Cons:** 依赖 DoclingReader 把 Word heading 转成 markdown `##`（实测 level 1 → `##`，level 2 → `###`，有偏移但相对关系正确）。

**3.3 image._data() 防御性 callable 检查**

- **Alternative A:** 直接调用 `image._data()`（假设是方法）。
- **Alternative B:** `image._data() if callable(image._data) else image._data`（兼容方法和属性两种情况）。
- **Choice:** Alternative B。
- **Rationale:** openpyxl 不同版本中 `_data` 可能是方法或属性，防御性代码避免版本兼容问题。review-it 审查认为冗余但可接受。

### 4. Open Questions

**4.1 PDF 标题检测阈值是否需调优**

- **Assumption:** 字号 ≥ body×2.0/1.5/1.25 对应 h1/h2/h3 是合理默认值。
- **Risk:** 真实 PDF 文档字号差异可能不符合这个比例（如学术文档 body=10pt，标题=12pt，比例仅 1.2x，不会被识别为标题）。
- **Follow-up:** 用真实业务 PDF 测试，如果标题漏检率高，考虑改为绝对阈值（如 ≥14pt → heading）或结合字体粗细判断。

**4.2 图片节点内联位置 vs. 末尾追加**

- **Assumption:** Word/Excel/PPT 图片追加到 nodes 列表末尾，page_number 用于近似定位。
- **Risk:** 如果文档有多页，第 1 页的图片会出现在所有文本节点之后，chunk_service 可能无法正确关联图片与周围文本。
- **Follow-up:** Issue #010 (chunk_service) 实现时验证图片位置是否影响检索质量。如果需要内联，改为在解析文本时按文档顺序插入图片节点。

**4.3 OCR 集成时机**

- **Assumption:** inference-service OCR 端点将在后续 sprint 部署。
- **Risk:** 如果 OCR 端点的 API 契约与 `ocr_api.py` 中的 `OCRResult` 模型不一致，需要调整。
- **Follow-up:** inference-service 部署后，验证 `/v1/ocr` 端点返回格式与 `OcrRegion`/`OCRResult` pydantic 模型一致，然后在 `_parse_pdf_lightweight` 中集成扫描页检测。

**4.4 DoclingReader heading level 偏移**

- **Observation:** DoclingReader 把 Word heading level 1 → markdown `##` (level 2)，level 2 → `###` (level 3)，整体偏移 1 级。
- **Risk:** 如果 chunk_service 依赖 heading_level 做切分，偏移可能导致层级判断错误。
- **Follow-up:** 确认 DoclingReader 的 heading 映射是否稳定。如果稳定，在 chunk_service 中按相对层级处理即可，无需修正绝对值。

---

## 验证命令

```bash
# 全部测试
cd ai/rag-engine && $env:PYTHONPATH = "."; python -m pytest tests/ -v
# 结果: 53 passed in 24s

# 架构验证
make validate-architecture
# 结果: architecture guardrails valid

# review-it
# 结果: review-it clean: no accepted/actionable findings remaining
```

## 备注

- 集群修复：本期额外修复了 dev-phys-03 节点的 containerd runtime 配置（nvidia → runc），恢复了 Kube-OVN 网络，使 MinIO/Milvus 服务从 StartError 恢复为 Running。这不是 issue-009 的范围，但是 E2E 测试的前置条件。
- 调试脚本（tests/print_*.py）已在 review-it 前清理，未进入 commit。
- `ocr_api.py` 中 `get_ocr_client()` 单例模式保留，后续 OCR 集成时使用。
