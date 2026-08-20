---
type: concept
title: Upgrade Operator (CRD Controller - Planned)
description: "ANIPatch CRD controller: planned. CI build scaffold exists in build-image.yml referencing the directory context, but no code directory on disk yet. Intended for online upgrade orchestration for inference services."
tags: [operators, upgrade, crd, controller, kubernetes, planned]
---

# Upgrade Operator (CRD Controller — Planned)

**Status**: Planned — CI build scaffold exists in `.github/workflows/build-image.yml` referencing `operators/upgrade-operator/` context, but the directory does not exist on disk yet.

Intended capabilities:
- **ANIPatch CRD** for inference service upgrade orchestration
- **Online upgrade** of inference services without downtime
- **Rollback support** on upgrade failure
- **Integration with InferenceOperator** for coordinated upgrade lifecycle

References:
- [Inference Operator](inference-operator.md) — Related CRD controller
- [Inference Contracts](../services-layer/inference.md) — Inference service contracts