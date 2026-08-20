---
type: concept
title: ANI Services Layer
description: "ANI Services layer scope, ownership matrix, API-first development workflow. Models, inference services, knowledge bases, PaaS, AI apps — the product layer above ANI Core."
tags: [services-layer, overview, ownership, workflow]
---

# ANI Services Layer

## Scope

ANI Services is the product layer that delivers cloud services using ANI Core as the infrastructure platform. Services calls Core via Core OpenAPI REST API / Core SDK — never via direct import.

| Domain | Services Responsibility |
|--------|------------------------|
| Model Repository | Model metadata, version management, import/export, publish policies |
| Inference Services | Inference deployment, OpenAI-compatible streaming, model routing, scaling |
| Knowledge Bases | Document management, chunking, indexing, RAG query, SSE streaming |
| PaaS Services | Managed databases, messaging, search, function compute, service catalog (future) |
| AI-Native Apps | Agent, MCP, industry applications, workflows (future) |

## Ownership Matrix

| Directory | Owner | Review Required |
|-----------|-------|----------------|
| `services/{model,kb,task,tenant,gateway,auth,metering,reconcile-worker}-service/` | Services | Core review for boundary violations |
| `frontends/{console,boss}/` | Services | Core review for API contract changes |
| `ai/rag-engine/` | Services | Core review for boundary violations |
| `operators/inference-operator/` | Services | Core review for API contract changes |
| `api/openapi/services/v1.yaml` | Services | Core co-review |
| `sdks/services/` | Services | Core co-review |

## API-First Development Workflow

```
1. Update services/v1.yaml (OpenAPI spec)
   └── Team review + Core co-review
2. Generate handler stubs + SDK types
   └── make gen-api, make gen-console-api
3. Implement handlers in services/ani-gateway/internal/router/
   └── Register via RegisterWithOptions
4. Implement backing service (Go gRPC or Python)
5. Run make validate-services
   └── API split gate, Services boundary gate, route contract gate, semantic contract gate,
       SDK drift detection, architecture gate, document entrypoint gate
6. Run live gate against real provider
```

## References

- [Core vs Services Boundary](../architecture/core-vs-services-boundary.md) — Hard boundary rules
- [Services API](services-api.md) — OpenAPI specification
- [ANI-SERVICES-TEAM-GUIDE.md](../../ANI-SERVICES-TEAM-GUIDE.md) — Team development guide
- Source: `repo/services/`, `repo/frontends/`, `repo/api/openapi/services/v1.yaml`