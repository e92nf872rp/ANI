---
type: reference
title: ANI CLI
description: "ANI CLI tool (cli/ani/): Go-based CLI using cobra/viper. Core API client with resource-aware routing. Subcommands for resource listing, CRUD operations. Services-aware routing (model, inference, kb prefixes use /api/v1/svc/). Mock server base URL (http://127.0.0.1:4010/api/v1)."
tags: [cli, command-line, tools, go]
---

# ANI CLI

**Directory**: `cli/ani/` · **Language**: Go · **Framework**: cobra/viper

## Overview

The ANI CLI is the command-line interface to the ANI Core API. It is built from the Core OpenAPI specification and uses auto-generated client code.

## Commands

| Command | Description |
|---------|-------------|
| `ani list <resource>` | List resources (instances, networks, volumes, etc.) |
| `ani get <resource> <id>` | Get resource by ID |
| `ani create <resource> -f <file>` | Create resource from JSON/YAML file |
| `ani delete <resource> <id>` | Delete resource |
| `ani --version` | Print version (text or JSON) |
| `ani --help` | Print help |

## Services-Aware Routing

Resources managed by ANI Services use the `/api/v1/svc/` prefix:
- `ani list models` → `GET /api/v1/svc/models`
- `ani list inference-services` → `GET /api/v1/svc/inference-services`
- `ani list knowledge-bases` → `GET /api/v1/svc/knowledge-bases`

## Configuration

| Env Variable | Flag | Default | Description |
|-------------|------|---------|-------------|
| `ANI_BASE_URL` | `--base-url` | `http://127.0.0.1:4010/api/v1` | Core API base URL |
| `ANI_TOKEN` | `--token` | - | Bearer token for authentication |

## Development

```bash
# Build CLI
make build-cli

# Run CLI with mock server
ANI_BASE_URL=http://127.0.0.1:4010/api/v1 ./bin/ani list instances
```

## References

- [Gateway](../core/gateway.md) — Gateway API entrypoint
- [Core API](../architecture/overview.md) — API structure
- [SDKs](../sdks/overview.md) — SDK architecture