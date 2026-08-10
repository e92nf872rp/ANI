# INSTANCE-SANDBOX-SUBRESOURCES-A · Sandbox Files Real-Provider

## Goal

Make Sandbox `ListFiles` / `WriteFile` / `DeleteFile` operate inside the ready Pod when `KubernetesSandboxRuntime` apply is enabled. Token / ports / checkpoint stay local-session. No OpenAPI changes. No GPU.

## Approach

Mirror code-run: `LocalSandboxRuntime` keeps validation and idempotency; when a file backend is injected, IO runs via `sandboxPodExecutor` under `/workspace/<relative-path>`.

## Success

- Unit tests cover write/list/delete + conflict/overwrite + path guard
- Live gate: create → write → list → delete → code-run → lifecycle; evidence `files-real; token/port/checkpoint deferred`
- Feature docs: development-record + CURRENT-SPRINT + ANI-06 + README
