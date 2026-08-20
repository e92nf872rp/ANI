# Files

- [Auth Service (gRPC)](auth-service.md) - Standalone gRPC authentication service: JWT signing/verification, OIDC/SSO login, refresh token management, API Key CRUD, password login, token blocklist, OIDC session management
- [Metering Service (gRPC)](metering-service.md) - Standalone gRPC service: periodic metering collection with per-resource ticker management, Prometheus collectors (CPU/mem), DCGM GPU collector, NATS lifecycle event consumer, startup rebuilder. Writes to Core DB metering_usage_records.
- [Reconcile Worker (Background Service)](reconcile-worker.md) - Standalone background worker running the reconcile controller loop: list targets, query provider status, reconcile stored state, with HA leader election and backoff management
- [Task Service (gRPC)](task-service.md) - Standalone gRPC service for async task CRUD: GetTask, CancelTask (unimplemented), UpdateTaskProgress. PostgreSQL-backed AsyncTaskRepo, outbox publisher, NATS JetStream integration.
- [Tenant Service (gRPC)](tenant-service.md) - Standalone gRPC service: tenant plan CRUD (draft/active/disabled), BindPlanQuota with 2PC (update plan_id → sync quota → rollback on failure), audit logging for quota plan changes. Uses Services-layer bootstrap (services/pkg/bootstrap/).
