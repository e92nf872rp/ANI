# Inference Service Stage C4 Gateway gRPC Ingress Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Reset inference ingress to the platform contract: ANI Gateway is the only HTTP entry; inference-service exposes a new internal gRPC `InferenceControl` API.

**Architecture:** Keep C1/C2 control plane. Delete the standalone `/api/v1/svc` HTTP mux. Gateway handlers inject `middleware.GetTenantID`, call gRPC, and emit approved OpenAPI JSON. Do not revive `InferenceServiceRPC`.

**Tech Stack:** Go 1.25, Hertz, gRPC, `api/proto/inference/control/v1`, generated `pkg/generated/pb/inference/control/v1`.

## Overturned

- `services/inference-service/internal/httpapi` product HTTP ingress
- Standalone service-identity TenantResolver / HMAC inbound JWT
- C4 HTTP composition root

## Kept

- Domain, PostgreSQL repository, Creator/Controller, reconciler, catalog/runtime ports and fakes
- Approved OpenAPI `#101` and Core `platform-workloads` `#99`
