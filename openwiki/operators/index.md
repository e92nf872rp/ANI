# Files

- [Inference Operator (CRD Controller)](inference-operator.md) - InferenceService CRD controller (Go operator): CRD types, phase state machine (Pending → Downloading → Decrypting → Deploying → Running → Stopping → Stopped/Failed), condition types, finalizer-based cleanup, vLLM integration.
- [Upgrade Operator (CRD Controller - Planned)](upgrade-operator.md) - ANIPatch CRD controller: planned. CI build scaffold exists in build-image.yml referencing the directory context, but no code directory on disk yet. Intended for online upgrade orchestration for inference services.
