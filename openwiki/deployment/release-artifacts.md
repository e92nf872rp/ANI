---
type: reference
title: Release Engineering Artifacts
description: "Release artifacts in deploy/release/: core-artifacts.yaml (SHA256 artifact manifest), core-hardening.yaml (hardening checklist), core-release-evidence.yaml (evidence index), core-version-policy.yaml (version numbering), sprint readiness assessments."
tags: [deployment, release, artifacts, versioning, hardening]
---

# Release Engineering Artifacts

**Directory**: `deploy/release/`

## Artifact Catalog

| File | Purpose |
|------|---------|
| `core-artifacts.yaml` | Release artifact manifest with SHA256 digests for every deliverable. Used for artifact drift detection. |
| `core-hardening.yaml` | Release hardening checklist: CVE scanning results, image signing, RBAC review, network policy verification. |
| `core-release-evidence.yaml` | Aggregate index of all release evidence: gate results, validation output, compliance checks. |
| `core-version-policy.yaml` | Version numbering scheme, compatibility policy, SemVer commitment, upgrade/downgrade rules. |
| `sprint10-core-final-readiness.yaml` | Sprint 10 final readiness assessment: gates completed, blockers, production-shaped status. |
| `sprint9-core-rc.yaml` | Sprint 9 RC readiness assessment. |

## Enforcement

- **Artifact drift detection**: `make validate-core-release-artifacts` — compares committed SHA256 digests against built artifacts
- **Version policy enforcement**: `make validate-core-version-policy` — validates SemVer compliance, compatibility declarations
- **Hardening verification**: `make validate-core-hardening` — checks hardening checklist completion

## References

- [Validation Gates](../ci-cd/validation-gates.md) — Gate catalog
- [Architecture Baselines](../ci-cd/architecture-baselines.md) — Governance baselines
- [Helm Charts](helm-charts.md) — Deployment packaging
- [Docker Compose](docker-compose.md) — Local development