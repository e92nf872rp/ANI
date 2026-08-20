---
type: concept
title: Inference Operator (CRD Controller)
description: "InferenceService CRD controller (Go operator): CRD types, phase state machine (Pending → Downloading → Decrypting → Deploying → Running → Stopping → Stopped/Failed), condition types, finalizer-based cleanup, vLLM integration."
tags: [operators, inference, crd, controller, kubernetes, vllm]
---

# Inference Operator (CRD Controller)

## Overview

**Directory**: `operators/inference-operator/` · **Language**: Go · **Framework**: Kubernetes controller-runtime

The Inference Operator manages the lifecycle of inference services as Kubernetes CRDs. It is responsible for model download, decryption, vLLM deployment, health checking, and graceful shutdown.

## CRD Types

### InferenceServiceSpec

| Field | Type | Description |
|-------|------|-------------|
| `model` | string | Model ID from ANI model registry, format `name:version` (immutable) |
| `replicas` | int32 | Number of vLLM pod replicas |
| `gpuType` | string | GPU type spec ID |
| `gpuCount` | int32 | GPUs per replica |
| `maxConcurrency` | int32 | Max concurrent requests per pod |
| `drainSeconds` | int32 | Graceful drain before shutdown |
| `encrypted` | bool | Model is encrypted (requires decryption) |
| `encryptAlgo` | string | Encryption algorithm (sm4, aes256gcm) |
| `engine` | string | Inference engine (vllm, trt-llm) |
| `engineArgs` | map[string]string | Engine-specific args |

### InferenceServiceStatus

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current phase (see state machine below) |
| `conditions` | []Condition | Detailed condition list |
| `modelDigest` | string | SHA256 digest of downloaded model |
| `endpoint` | string | Service endpoint URL |
| `observedGeneration` | int64 | Last reconciled generation |

## Phase State Machine

```
Pending → Downloading → Decrypting → Deploying → Running → Stopping → Stopped / Failed
```

Forbidden transitions:
- Any state → Running (only from Deploying via health-check event)
- Stopped → anything (terminal; must delete and recreate)
- Failed → anything (terminal; must delete and recreate)

## Condition Types

| Condition | Description |
|-----------|-------------|
| `ModelReady` | Model downloaded (and decrypted if encrypted) |
| `PodScheduled` | vLLM pod exists and is scheduled |
| `Healthy` | vLLM health endpoint returns 200 |
| `DrainComplete` | All in-flight requests finished before stop |

## Finalizer

**Name**: `ani.kubercloud.io/inference-service-cleanup`

Behavior: Set on creation. Controller removes only after all owned resources (Deployment, Service, ConfigMap, decryption Secret) are deleted and in-flight requests are drained.

## References

- [Inference Contracts](../services-layer/inference.md) — Service create/update contracts
- [Model Service](../services-layer/model-service.md) — Model registry consumed by operator
- Source: `operators/inference-operator/api/v1/inferenceservice_types.go`