---
type: reference
title: Docker Compose Local Dev Environment
description: "Local development environment via Docker Compose (deploy/docker/): PostgreSQL, NATS, Redis, MinIO, Milvus, Harbor. Makefile integration. Alternative startup script start.sh."
tags: [deployment, docker-compose, local-dev, make]
---

# Docker Compose Local Dev Environment

**Directory**: `deploy/docker/`

## Overview

The Docker Compose environment provides a local development setup with all backing services needed for ANI Core development.

## Services

| Service | Image | Port | Purpose |
|---------|-------|------|---------|
| PostgreSQL | postgres:16 | 5432 | Metadata store, async tasks, outbox |
| NATS | nats:latest | 4222 | Message bus, JetStream |
| Redis | redis:7 | 6379 | Cache, token blocklist, rate limiting |
| MinIO | minio/minio | 9000/9001 | Object store |
| Milvus | milvus:latest | 19530 | Vector store |
| Harbor | goharbor/harbor | 80/443 | Container image registry |
| Prometheus | prom/prometheus | 9090 | Metrics collection |
| Loki | grafana/loki | 3100 | Log aggregation |

## Makefile Integration

| Command | Description |
|---------|-------------|
| `make deps` | Start all dependency services |
| `make deps-down` | Stop all dependency services |
| `make deps-status` | Show running status |
| `make deps-clean` | Stop and clear all data |

## Alternative Startup Script

`repo/start.sh` (17KB) provides an alternative startup method for environments without `make` or Docker Compose. Supported modes:

| Mode | Description |
|------|-------------|
| `setup` | Install dependencies |
| `build` | Compile services |
| `bg` | Start background services |
| `stop` | Stop background services |
| `status` | Check running status |
| `logs` | Tail logs |
| `interactive` | TUI with quit/exit/Ctrl-C handling |

## Hot-Reload and Live Dev Workflow

**Hot-reload is not configured.** There is no `air.toml`, `watz`, `fswatch`, or equivalent hot-reload setup in the repository. Developers must manually restart services after code changes using `make` targets or `start.sh`.

### Mock/In-Memory Services

The codebase provides in-memory adapter implementations (`Local*` variants) for local development that avoid real infrastructure dependencies:

| Local Adapter | Replaces | Source |
|--------------|----------|--------|
| `LocalSecretService` | K8s Secret Provider | `pkg/adapters/runtime/local_secret_service.go` |
| `LocalEncryptionService` | KMS Encryption Provider | `pkg/adapters/runtime/local_encryption_service.go` |
| `LocalGPUSchedulingQueueStore` | Volcano queue (for dev) | `pkg/adapters/runtime/` |
| `LocalStorageService` | Ceph/Rook storage | `pkg/adapters/runtime/` |

These adapters are selected via configuration/environment and operate in-memory without database or external service requirements.

## References

- [Helm Charts](helm-charts.md) — Production deployment profiles
- [Installer](installer.md) — Offline TUI installer
- [Real K8s Lab](real-k8s-lab.md) — Production-shaped live gates
- [Build System](../ci-cd/build-system.md) — Makefile targets
- Source: `deploy/docker/`, `repo/start.sh`