---
type: concept
title: Document Parser (Python - Planned)
description: "Python document parser service (ai/doc-parser/): planned. CI build scaffold exists in build-image.yml referencing the directory context, but no code directory on disk yet. Intended for Docling-based parsing with PaddleOCR integration."
tags: [ai-services, doc-parser, planned, docling, ocr]
---

# Document Parser (Python — Planned)

**Status**: Planned — CI build scaffold exists in `.github/workflows/build-image.yml` referencing `ai/doc-parser/` context, but `ai/doc-parser/` directory does not exist on disk yet.

Intended capabilities:
- **Docling-based document parsing**: PDF, DOCX, XLSX, TXT, MD → structured chunks
- **PaddleOCR integration**: OCR processing for scanned documents and images
- **NATS worker integration**: async parsing triggered by `ani.tasks.kb.parse` subject
- **Chunk output format**: compatible with RAG engine chunk service

Refer to [RAG Engine](rag-engine.md) for the currently operational document parsing pipeline.