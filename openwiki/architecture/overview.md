---
type: concept
title: ANI Architecture Overview
description: "Two-layer architecture of the KuberCloud ANI platform: ANI Core (infrastructure control plane) and ANI Services (cloud services and orchestration layer)"
tags: [architecture, core, services, boundary]
---

# ANI Architecture Overview

## Two-Layer Architecture

ANI is an AI Private Cloud platform split into two strict layers:

1. **ANI Core** — Infrastructure control plane. Manages compute instances (VM, container, GPU, sandbox, K8s cluster), network (VPC, subnet, security groups), storage (block, file, object, vector), identity/auth/RBAC, observability, metering, async tasks, quota, and reconcile controllers. Written in Go. Communicates via Core OpenAPI REST API and internal gRPC.

2. **ANI Services** — Cloud services and user-facing orchestration layer. Provides model registry, inference services, knowledge bases, RAG engine, PaaS hosting, Console/BOSS frontends, and AI-native applications. Uses Go (gRPC), Python (AI services), and TypeScript (frontends). Consumes Core capabilities exclusively through Core OpenAPI/SDK — never through direct code imports.

## Architecture Diagram

```mermaid
flowchart TB
    users["Users / Admins / Operators"]
    tools["Console / BOSS / CLI / SDK / Third-party systems"]

    subgraph contracts["API Contract Layer"]
        coreApi["Core OpenAPI REST API\nrepo/api/openapi/v1.yaml\n/api/v1"]
        svcApi["Services REST API\nrepo/api/openapi/services/v1.yaml\n/api/v1/svc"]
        coreSdk["Core SDK\nGo / Python / TypeScript / Java"]
        svcSdk["Services SDK\nGo / Python / TypeScript / Java"]
    end

    subgraph core["ANI Core: Infrastructure Platform"]
        gateway["ANI Gateway\nAuth / RBAC / Audit / Routing / Error standardization"]
        coreServices["Core Services\nInstances / Network / Storage / K8s Cluster / Encryption / Secrets / Auth / Metering"]
        ports_adapters["pkg/ports + pkg/adapters\nCapability abstractions + default providers"]
        controllers["Reconcile Controllers\nStatus observation / state reconciliation"]
    end

    subgraph services["ANI Services: Cloud Services & Orchestration"]
        console_boss["Console / BOSS\nUser interaction / admin operations"]
        cloudSvc["IaaS Cloud Services\nCloud hosts / containers / K8s / network / storage"]
        paasSvc["PaaS Managed Services\nDatabases / messaging / search / functions"]
        aiLifecycle["AI Lifecycle Services\nModels / datasets / notebooks / training / fine-tuning"]
        aiApp["AI-Native Services\nInference / knowledge bases / Agent / MCP"]
        svcOrch["Services Orchestration\nFlows / permissions / metering / canary / rollback"]
    end

    subgraph foundation["Real Provider Layer"]
        k8s["Kubernetes"]
        kubevirt["KubeVirt"]
        kubeovn["Kube-OVN"]
        vcluster["vCluster"]
        hami_volcano["HAMi / Volcano"]
        data_services["PostgreSQL / MinIO / Milvus / NATS / Redis / Harbor"]
        kms["KMS / SM4 / K8s Secrets"]
    end

    users --> tools
    tools --> coreApi & svcApi
    coreApi --> gateway
    svcApi --> gateway
    coreSdk --> coreApi
    svcSdk --> svcApi
    gateway --> coreServices
    tools --> console_boss
    console_boss --> svcApi
    gateway --> cloudSvc & paasSvc & aiLifecycle & aiApp & svcOrch
    coreServices --> ports_adapters --> foundation
    controllers --> ports_adapters
    services --> coreSdk
```

## Key Architectural Rules

1. **Two separate API contracts**: Core API (`/api/v1`) and Services API (`/api/v1/svc`) are distinct OpenAPI specs with separate SDKs.
2. **Directional dependency**: Services may use Core SDK; Core must never import Services code.
3. **Gateway routing split**: Core routes (`/api/v1/*`) and Services routes (`/api/v1/svc/*`) are registered in the same Gateway process but owned by different teams.
4. **Ports/adapters**: All infrastructure capabilities are abstracted through `pkg/ports/` interfaces with default `pkg/adapters/` implementations. No service code depends on specific provider SDKs.
5. **API-first development**: Changes start in the OpenAPI spec, then handler implementation, then SDK generation, then validation gate.

## References

- [Core vs Services Boundary](core-vs-services-boundary.md) — Strict boundary rules
- [Ports and Adapters](ports-and-adapters.md) — Capability abstraction pattern
- [Tech Stack](tech-stack.md) — Technology selection decisions
- ANI-05: System Architecture Design (repo root `ANI-05-系统架构设计.md`)
- [Core Overview](../core/overview.md) — Core platform capabilities
- [Services Layer Overview](../services-layer/overview.md) — Services capabilities