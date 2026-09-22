# Model Stream Import Implementation Plan

> **For agentic workers:** Implement task-by-task with tests and no repository commit unless explicitly requested.

**Goal:** Import arbitrarily large public model repositories without a local tar archive, streaming each immutable source file into MinIO and publishing a verified manifest.

**Architecture:** The durable PostgreSQL import task remains the authority. A worker resolves one immutable revision, persists a file manifest, uploads each file through bounded-memory multipart streaming, and writes a manifest object. The existing model version points to that manifest; legacy archive versions remain readable.

**Tech Stack:** Go, PostgreSQL/pgx, existing ports/adapters, MinIO S3 multipart API, ModelScope/Hugging Face HTTP Range.

## Global Constraints

- Keep the public ImportModel contract compatible.
- Do not use a local complete-model directory or tar.gz staging file.
- Every database mutation is tenant-scoped and lease/fence guarded.
- Never claim a version ready before every file and manifest checksum are verified.
- Preserve legacy `model.tar.gz` consumption.
- Log structured task/file progress without secrets or signed URLs.

## Tasks

- Add durable file rows and upload checkpoints to the existing model import migration/repository.
- Add bounded-range source reads and optional multipart object-store adapter.
- Stream worker files to object storage, persist progress, write `manifest.json`, and complete the model version only after verification.
- Update model materialization/download authorization and fetcher to consume manifests while retaining archive compatibility.
- Add structured progress logs, failure model status updates, tests, and real PostgreSQL/MinIO verification.
