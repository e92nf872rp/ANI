---
type: reference
title: GitHub Actions CI/CD
description: "GitHub Actions workflows (.github/workflows/): PR validation, sprint gate validation, release workflows. Build matrix for services, operators, AI services. Integration with Makefile validation targets."
tags: [ci-cd, github-actions, workflows, ci, cd]
---

# GitHub Actions CI/CD

**Directory**: `.github/workflows/`

## Workflows

| Workflow | Trigger | Purpose |
|----------|---------|---------|
| `pr-validation.yml` | pull_request | Run lint, test, validate-architecture, validate-services, build matrix |
| `sprint-gate-validation.yml` | workflow_dispatch | Run all sprint gates (validate-sprint13-b-track-production-shape, etc.) |
| `release.yml` | tag push | Build images, run release gates, push to registry |

## Build Matrix

The image build workflow builds all services in parallel:

| Layer | Services |
|-------|----------|
| **Core Services** | gateway, auth-service, model-service, task-service, reconcile-worker |
| **CLI** | ani-cli |
| **AI Services** | rag-engine, doc-parser (scaffold), whisper-service (scaffold) |
| **Operators** | inference-operator, upgrade-operator (scaffold) |

## References

- [Build System](build-system.md) — Makefile integration
- [Validation Gates](validation-gates.md) — Gate targets
- [Helm Charts](../deployment/helm-charts.md) — Deployment packaging