---
type: reference
title: Frontend Module Documentation
description: "Index of Services-layer frontend module governance docs at services/docs/console-modules/ and services/docs/boss-modules/ — status boards, completion matrices, delivery workflows, per-module PRDs/SPECs, Phase 3 OpenAPI drafts, gap analyses. Also: HTML prototypes and generated API documentation."
tags: [frontends, module-docs, governance, api-docs]
---

# Frontend Module Documentation

## Module Governance Docs

The authoritative documentation for frontend module structure lives in `services/docs/`:

### Console Modules (`services/docs/console-modules/`)

| Asset | Path |
|-------|------|
| Governance README | `governance/README.md` |
| Status Board | `governance/console-document-status-board.md` |
| Module Completion Matrix | `governance/module-completion-matrix.md` |
| Delivery Workflow | `governance/module-delivery-workflow.md` |
| Undefined Features Backlog | `governance/console-undefined-features-backlog.md` |
| Schema Completion Tracker | `governance/schema-completion-tracker.md` |
| Closeout Checklist | `governance/governance-closeout-checklist.md` |
| YAML Expansion Summary | `governance/YAML-EXPANSION-SUMMARY-2026-06-17.md` |
| Final Handoff Summary | `governance/final-handoff-summary.md` |

Module directories: `compute/`, `inference/`, `knowledge/`, `home/`, `ai-native/`, etc.

### BOSS Modules (`services/docs/boss-modules/`)

| Asset | Path |
|-------|------|
| Governance README | `governance/README.md` |
| Status Board | `governance/boss-document-status-board.md` |
| Full Depth Checklist | `governance/boss-full-depth-checklist.md` |
| Phase 0 Gap Audit | `governance/boss-phase0-gap-audit/` (8 gap files) |
| Phase 2 YAML Expansion | `governance/boss-phase2-yaml-expansion-summary.md` |
| Module Completion Matrix | `governance/module-completion-matrix.md` |
| Delivery Workflow | `governance/module-delivery-workflow.md` |

Module directories: `overview/`, `tenant/`, `ops/`, `metering/`, `audit/`, `health/`, etc.

## HTML Prototypes

`services/prototypes/` contains early-stage UX HTML prototypes:

| File | Size | Purpose |
|------|------|---------|
| `ani-services-prototype-console.html` | 735KB | Console page layout and interaction patterns |
| `ani-services-prototype-boss.html` | 657KB | BOSS page layout and interaction patterns |
| `ani-services-prototype.html` | 754KB | Combined/generic prototype |

These are **historical UX references**. The authoritative module structure is defined by the governance docs above.

## Generated API Documentation

`repo/docs/api/` contains static HTML pages generated from OpenAPI specs:

| File | Source Spec |
|------|-------------|
| `core.html` | Core OpenAPI (`api/openapi/v1.yaml`) |
| `services.html` | Services OpenAPI (`api/openapi/services/v1.yaml`) |
| `index.html` | Landing page linking both |

Regeneration: `make gen-api-docs`

## Installer Documentation

`repo/docs/installer/`:

| File | Purpose |
|------|---------|
| `ssh-trust-installer.md` | SSH trust setup guide for the offline installer |

## References

- [Console Portal](console.md) — User Console overview
- [BOSS Portal](boss.md) — Admin portal overview
- [Services Layer Overview](../services-layer/overview.md) — API-first development workflow
- Source: `services/docs/`, `services/prototypes/`, `repo/docs/api/`, `repo/docs/installer/`