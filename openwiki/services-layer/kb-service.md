---
type: service
title: Knowledge Base Service (gRPC)
description: "Python gRPC service for knowledge base management: 10 P0 RPCs (CRUD, doc management, chunk query, search), asyncpg dual-loop architecture, outbox dispatcher, Redis session cache, Core API client, RAG engine client, pg_trgm keyword retrieval. Fully implemented with 9 test files."
tags: [services-layer, kb, knowledge-base, gRPC, python, asyncpg, redis, outbox]
---

# Knowledge Base Service (gRPC)

## Overview

**Service**: `services/kb-service/` · **Language**: Python · **Framework**: FastAPI + gRPC (dual event loop)

A fully implemented Python gRPC service managing knowledge base lifecycle. Not a placeholder — 10 P0 RPCs, asyncpg dual-pool architecture, outbox dispatcher, Redis session cache, Core API client, and RAG engine client are all implemented with 9 test files.

## Architecture

### Dual Event Loop Design

```text
┌──────────────────────────────────────────────────────────────┐
│                    ani-kb-service Process                      │
│                                                               │
│  gRPC Event Loop (background thread)                          │
│  ├── asyncpg Pool A (bound to gRPC loop)                      │
│  ├── gRPC servicer methods run on ThreadPoolExecutor          │
│  │   └── each submits coroutines to gRPC loop via              │
│  │       run_coroutine_threadsafe                              │
│  └── KBServiceServicer (10 RPCs)                               │
│                                                               │
│  Uvicorn Event Loop (main thread)                              │
│  ├── asyncpg Pool B (bound to uvicorn loop)                    │
│  ├── OutboxDispatcher (background asyncio task)                │
│  ├── Health/Readiness endpoints (FastAPI)                      │
│  └── NATS connection (best-effort, graceful degradation)       │
└──────────────────────────────────────────────────────────────┘
```

Pool separation is required because asyncpg pools are bound to the event loop they are created on. gRPC methods run on `ThreadPoolExecutor` threads, not the uvicorn loop, so they need a dedicated pool on the gRPC loop.

### Startup Sequence

```python
# main.py startup order:
1.  Start gRPC event loop on background thread
2.  Create asyncpg Pool A ON THE gRPC LOOP (for RPC DB access)
3.  Create asyncpg Pool B ON THE UVICORN LOOP (for outbox dispatcher)
4.  Connect NATS (best-effort; service starts without it)
5.  Start OutboxDispatcher coroutine on uvicorn loop
6.  Start gRPC server on background thread with Pool A
7.  FastAPI serves health/readiness on main thread
```

## gRPC API (10 P0 RPCs)

Proto: `api/proto/kb/v1/kb_service.proto`

| RPC | Description |
|-----|-------------|
| `CreateKnowledgeBase` | Create KB with name, description, embedding config |
| `GetKnowledgeBase` | Get KB by ID (with document count, chunk count) |
| `ListKnowledgeBases` | List KBs with cursor pagination, status filter |
| `UpdateKnowledgeBase` | Update KB metadata (name, description, embedding config) |
| `DeleteKnowledgeBase` | Soft-delete KB (cascades to documents + chunks) |
| `ListDocuments` | List documents in KB with pagination, status filter |
| `DeleteDocument` | Soft-delete document (cascades to chunks) |
| `GetDocumentChunks` | Get paginated chunks for a document |
| `SearchChunks` | Keyword + vector hybrid search over KB chunks |
| `GetKBStatistics` | Aggregate statistics: doc count, chunk count, index status, storage used |

## Key Subsystems

### OutboxDispatcher (`app/outbox/dispatcher.py`)

- Polls `outbox_events` table with `SELECT ... FOR UPDATE SKIP LOCKED`
- Publishes to NATS `ani.tasks.kb.parse` subject when a document is uploaded
- Config: `OUTBOX_POLL_INTERVAL_SECONDS` (default 1s), `OUTBOX_MAX_BATCH_SIZE` (default 50)
- Dead letter: after `OUTBOX_MAX_ATTEMPTS` (default 10), sets `picked_at` and leaves for inspection

### Core API Client (`app/core_api/client.py`)

Wraps Core Go SDK REST API for:
- Instance observability (Prometheus query proxy, Loki log query)
- Token validation
- Quota check before KB creation

### RAG Engine Client (`app/rag_engine/client.py`)

gRPC client to `ai/rag-engine/` for:
- Document parsing status queries
- Embedding dimension validation
- Index health checks

### Redis Session Cache (`app/session/cache.py`)

- Config: `REDIS_URL`, `SESSION_CACHE_TTL_SECONDS` (default 3600)
- Used for: KB query session state, streaming cursor tracking

## Gateway Integration

### gRPC Client (`services/ani-gateway/internal/router/kb_grpc_client.go`)

Gateway talks to kb-service over gRPC. Provides `KBGRPCClient` interface used by `RegisterWithOptions`.

### SSE Streaming (`services/ani-gateway/internal/router/kb_sse.go`)

Gateway SSE endpoint for KB streaming queries. Full handler flow:

```text
1. Pre-stream validation: question param (max 2000 chars) → 400 if invalid
2. Pre-stream errors (400/401/404) → return JSON directly (no stream)
3. RagClient retrieval (synchronous) → SourceChunk list
4. Prompt construction from sources + question
5. vLLM /v1/chat/completions stream=true → token events
6. On stream completion → emit `sources` event (JSON SourceChunk list)
7. Emit `done` event
8. Mid-stream error → emit SSE `error` event and close stream
```

**Degradation**: When `RagClient` or `VLLMStreamer` is nil (not configured), the handler emits an empty token stream (`sources=[]` + `done`) so the endpoint stays functional without backend services.

**Timeouts**: The synchronous Query RPC uses a **120-second per-RPC timeout** (kb_grpc_client.go: `queryRPCTimeout = 120s`), exceeding the generic 5-second timeout because RAG retrieval + vLLM streaming may take longer than other RPCs.

**Test coverage**: `kb_sse_test.go` validates SSE event sequence with fakes for both RAG and vLLM backends. `tests/e2e/run_e2e_sse_test.py` provides end-to-end SSE validation.

## Tests

9 test files totalling ~100KB:
- `test_create_kb.py`, `test_get_kb.py`, `test_list_kbs.py` — KB CRUD
- `test_document_upload.py`, `test_document_delete.py` — Document management
- `test_chunk_query.py`, `test_search.py` — Chunk retrieval
- `test_outbox_dispatcher.py` — Outbox polling + NATS publishing
- `test_integration.py` — Cross-service integration scenarios

## References

- [Services API](services-api.md) — Services OpenAPI spec for KB endpoints
- [RAG Engine](../ai-services/rag-engine.md) — Python RAG engine for document parsing and vector search
- [Inference](inference.md) — Streaming proxy pattern (same SSE pattern used here)
- Source: `services/kb-service/`, `api/proto/kb/v1/`, `services/ani-gateway/internal/router/kb_*.go`