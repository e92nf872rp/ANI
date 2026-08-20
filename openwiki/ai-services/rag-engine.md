---
type: concept
title: RAG Engine (Python)
description: "Python RAG engine (ai/rag-engine/): FastAPI-based microservice with gRPC server. Milvus vector store for document retrieval. Text2vec embeddings. Document chunking/indexing. Query/QA service with SSE streaming. NATS worker for async parsing."
tags: [ai-services, rag-engine, python, milvus, gRPC, sse]
---

# RAG Engine (Python)

## Overview

**Directory**: `ai/rag-engine/` · **Language**: Python (FastAPI + gRPC)

The RAG engine provides document parsing, embedding, indexing, and retrieval capabilities for the Knowledge Base service. It operates as a backend microservice called by `kb-service` via gRPC.

## Architecture

```
ai/rag-engine/
├── main.py                       # FastAPI app entrypoint + gRPC server bootstrap
├── app/
│   ├── __init__.py
│   ├── routers/
│   │   ├── __init__.py
│   │   ├── documents.py          # Document upload/status/delete endpoints
│   │   └── query.py              # Query/QA endpoints with SSE streaming
│   ├── services/
│   │   ├── __init__.py
│   │   ├── chunk_service.py      # Document chunking logic
│   │   ├── embed_service.py      # Text embedding generation
│   │   ├── parse_service.py      # Document parsing orchestration
│   │   ├── retrieve_service.py   # Vector + keyword retrieval
│   │   ├── qa_service.py         # Question answering over retrieved chunks
│   │   └── summary_service.py    # Document summarization
│   ├── workers/
│   │   ├── __init__.py
│   │   └── parse_worker.py       # NATS-based async document parse worker
│   ├── clients/
│   │   ├── __init__.py
│   │   ├── core_api.py           # ANI Core API client (instance lookup, etc.)
│   │   ├── minio_client.py       # MinIO document storage access
│   │   └── ocr_api.py            # OCR API client for image-based documents
│   ├── grpc/
│   │   ├── __init__.py
│   │   ├── server.py             # gRPC server registration
│   │   ├── rag_pb2.py            # Generated protobuf types
│   │   └── rag_pb2_grpc.py       # Generated gRPC stubs
│   ├── core/
│   │   ├── __init__.py
│   │   ├── config.py             # Configuration from env
│   │   ├── embeddings.py         # Embedding model management
│   │   └── milvus.py             # Milvus connection pool and operations
│   └── repositories/
│       ├── __init__.py
│       └── chunks.py             # Chunk storage and retrieval
└── tests/
    ├── test_chunk_service.py
    ├── test_embed_service.py
    ├── test_e2e_parse.py
    ├── test_parse_service.py
    ├── test_parse_worker_and_grpc.py
    ├── test_qa_service.py
    ├── test_retrieve_service.py
    └── test_summary_service.py
```

## Key Workflows

### Document Parse Pipeline

```
Upload → (kb-service) → NATS ani.tasks.kb.parse → parse_worker → ParseService → ChunkService → EmbedService → Milvus upsert
```

### Query Flow

```
User query → (kb-service gRPC) → RetrieveService (vector + keyword) → QAService → SSE stream → (Gateway) → Console
```

## References

- [KB Service](../services-layer/kb-service.md) — Calls RAG engine via gRPC
- [Services API](../services-layer/services-api.md) — KB API contract
- Source: `ai/rag-engine/`