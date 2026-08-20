---
type: concept
title: Inference Subsystem
description: "Inference subsystem: OpenAI-compatible streaming proxy (/v1/chat/completions), InferenceService CRD operator with phase state machine, model download/decrypt (SM4/AES), vLLM deployment. Inference contracts C27/C35."
tags: [services, inference, vllm, crd, operator, streaming]
---

# Inference Subsystem

## Architecture

<!-- openwiki: mermaid parse failed and this diagram was converted to a text fence so it does not break rendering. Fix the diagram source and restore the mermaid fence. Parser error: Heuristic: an unescaped angle bracket inside a label breaks rendering; rephrase the label. -->
```text
flowchart LR
    Client["Client (OpenAI SDK/Console)"] --> Proxy["Gateway /v1/chat/completions"]
    Proxy --> CRD["InferenceService CRD<br/>(K8s Operator)"]
    CRD --> Download["Model Download"]
    Download --> Decrypt["Model Decrypt (SM4/AES)"]
    Decrypt --> vLLM["vLLM Deployment"]
    vLLM --> Proxy
    Proxy --> Client
```

## Components

### OpenAI-Compatible Streaming Proxy

**Files**: `services/ani-gateway/internal/router/inference_resources.go`

| Endpoint | Description |
|----------|-------------|
| `POST /v1/chat/completions` | OpenAI-compatible chat completions (streaming via SSE) |
| `GET /v1/inference/stream` | Inference streaming endpoint |

### InferenceService CRD Operator

**Directory**: `operators/inference-operator/` · **Language**: Go

#### CRD Types

| Type | File | Description |
|------|------|-------------|
| `InferenceServiceSpec` | `api/v1/inferenceservice_types.go` | Model ID, replicas, GPU config, engine args |
| `InferenceServiceStatus` | `api/v1/inferenceservice_types.go` | Phase, conditions, observed generation |

#### Phase State Machine

```
Pending → Downloading → Decrypting (if encrypted) → Deploying → Running
                                                                      ↓
                                                               Stopping → Stopped
Any phase → Failed (unrecoverable error, max retries)
```

#### Condition Types

| Condition | Meaning |
|-----------|---------|
| `ModelReady` | Model downloaded (and decrypted if encrypted) |
| `PodScheduled` | vLLM pod exists and is scheduled |
| `Healthy` | vLLM health endpoint returns 200 |
| `DrainComplete` | All in-flight requests finished before stop |

#### Finalizer

`ani.kubercloud.io/inference-service-cleanup` — ensures all owned resources (Deployment, Service, ConfigMap, Secrets) are deleted before CRD removal.

### Inference Contracts

| Contract | Description | Status |
|----------|-------------|--------|
| C27 | Inference service create contract | Spec merged, handler pending |
| C35 | Engine extra args contract | Spec merged, handler pending |
| Platform-workloads additive v1 | Phase A contract | Spec merged |

## References

- [Inference Operator](../operators/inference-operator.md) — CRD controller
- [KB Service](kb-service.md) — Related knowledge base service
- [API Contract](services-api.md) — Services OpenAPI
- [Sprint Tracking](../development/sprint-tracking.md) — Current status
- Source: `services/ani-gateway/internal/router/inference_resources.go`, `operators/inference-operator/`