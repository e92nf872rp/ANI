# Files

- [Gateway (Services-Layer Perspective)](gateway.md) - ANI Gateway: unified HTTP entry point for Core and Services APIs, middleware chain, route registration with nil-provider guards, services-layer route groups (/api/v1/svc/*), SSE streaming proxy, auth middleware integration for services-layer APIs.
- [Inference Subsystem](inference.md) - Inference subsystem: OpenAI-compatible streaming proxy (/v1/chat/completions), InferenceService CRD operator with phase state machine, model download/decrypt (SM4/AES), vLLM deployment. Inference contracts C27/C35.
- [Knowledge Base Service (gRPC)](kb-service.md) - Python gRPC service for knowledge base management: 10 P0 RPCs (CRUD, doc management, chunk query, search), asyncpg dual-loop architecture, outbox dispatcher, Redis session cache, Core API client, RAG engine client, pg_trgm keyword retrieval. Fully implemented with 9 test files.
- [Model Service (gRPC)](model-service.md) - Standalone gRPC service: model CRUD (CreateModel, GetModel, ListModels, SoftDeleteModel), model version management (CreateVersion, ListVersions). PostgreSQL-backed PostgresModelRepo. Proto definition at api/proto/model/v1/.
- [ANI Services Layer](overview.md) - ANI Services layer scope, ownership matrix, API-first development workflow. Models, inference services, knowledge bases, PaaS, AI apps — the product layer above ANI Core.
- [Services OpenAPI & SDK](services-api.md) - Services OpenAPI specification (api/openapi/services/v1.yaml), SDK generation from services contract, API-first development workflow with validation gates
