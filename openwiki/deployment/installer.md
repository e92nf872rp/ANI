---
type: concept
title: Offline Installer
description: "ANI offline installer (installer/ani-installer/): Go bubbletea TUI installer wizard, offline package management, preflight checks, release packaging."
tags: [deployment, installer, tui, offline, bubbletea]
---

# Offline Installer

**Directory**: `installer/ani-installer/` · **Language**: Go · **Framework**: bubbletea TUI

## Capabilities

- **Interactive wizard** for offline installation of ANI Core platform
- **Preflight checks**: K8s cluster validation, storage class detection, node requirements
- **Offline package management**: `deploy/offline/core-package.yaml` + `core-package-lock.yaml`
- **SSH trust setup**: documented in `repo/docs/installer/ssh-trust-installer.md`

## References

- [Helm Charts](helm-charts.md) — Platform umbrella chart
- [Docker Compose](docker-compose.md) — Local dev environment
- [Real K8s Lab](real-k8s-lab.md) — Production deployment manifests