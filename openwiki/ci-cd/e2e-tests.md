---
type: reference
title: End-to-End Integration Tests
description: "E2E test suite at repo/tests/e2e/: run_e2e_sse_test.py (15.7KB) orchestrating real rag-engine, Gateway, and vLLM for SSE streaming validation. fake_rag_engine.py (1.4KB) for isolated SSE testing."
tags: [ci-cd, testing, e2e, sse, integration]
---

# End-to-End Integration Tests

**Directory**: `repo/tests/e2e/`

## SSE Streaming E2E Test

**File**: `run_e2e_sse_test.py` (15.7KB)

This test orchestrates three real services to validate the complete SSE streaming path:

```mermaid
sequenceDiagram
    participant Test as E2E Test Runner
    participant Gateway as ANI Gateway
    participant RAG as RAG Engine
    participant vLLM as vLLM Inference

    Test->>Gateway: POST /api/v1/svc/knowledge-bases/{id}/query (stream=true)
    Gateway->>RAG: gRPC QueryKB (streaming)
    RAG->>vLLM: Embedding/retrieval
    RAG-->>Gateway: Streaming chunks (gRPC)
    Gateway-->>Test: SSE events (text/event-stream)
    Test->>Test: Validate SSE event sequence, content, termination
```

Covers: KB document upload → parse → embed → index → streaming query → SSE response delivery through Gateway proxy.

## Fake RAG Engine

**File**: `fake_rag_engine.py` (1.4KB)

Lightweight RAG engine replacement for isolated SSE endpoint testing without real RAG engine dependencies. Returns controlled streaming responses for deterministic test assertions.

## Running

```bash
# Run all E2E tests
cd repo && make test-e2e

# Run SSE-specific E2E test
cd repo/tests/e2e && python run_e2e_sse_test.py
```

## References

- [KB Service](../services-layer/kb-service.md) — Knowledge base gRPC service
- [RAG Engine](../ai-services/rag-engine.md) — Python RAG engine
- [Inference](../services-layer/inference.md) — Inference contracts
- [Validation Gates](validation-gates.md) — Live gate catalog