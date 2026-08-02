#!/usr/bin/env python3
from pathlib import Path
import sys


ROOT = Path(__file__).resolve().parents[1]


def require(path: str, needles: list[str]) -> list[str]:
    text = (ROOT / path).read_text(encoding="utf-8")
    return [f"{path}: missing {needle}" for needle in needles if needle not in text]


def main() -> int:
    errors = []
    errors += require("pkg/ports/async_task.go", ["type AsyncTaskStore interface", "Create(", "Get(", "Update("])
    errors += require("pkg/adapters/runtime/async_task_store.go", ["MetadataAsyncTaskStore", "WithTenantTx", "INSERT INTO async_tasks", "result"])
    errors += require("services/ani-gateway/internal/router/task_resources.go", ["registerTasksWithStore", "api.store.Get", "api.store.Update"])
    errors += require("deploy/migrations/20260802_001_async_tasks.sql", ["result JSONB", "idx_async_tasks_tenant_idempotency", "idx_async_tasks_tenant_id"])
    task_router = (ROOT / "services/ani-gateway/internal/router/task_resources.go").read_text(encoding="utf-8")
    if "completedTasks" in task_router:
        errors.append("task_resources.go: package-level completedTasks authority remains")
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print("async task store validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
