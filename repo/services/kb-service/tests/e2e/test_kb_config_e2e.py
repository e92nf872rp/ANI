"""E2E test for B5 issue #22 — GET /knowledge-bases/{kb_id}/config,
exercising the FULL local chain:

  REST client → ani-gateway (:8080, repo/bin/ani-gateway.exe)
             → kb-service gRPC (:50053, local main.py)
             → PostgreSQL (server NodePort 30945, real RLS)

The endpoint under test (kb-p1-plan §3 B5 #22):
  GET  /knowledge-bases/{kb_id}/config  (getKnowledgeBaseConfig)

Business flow (real KB row, no document upload needed — config is pure
metadata on the knowledge_bases row):
  1. CreateKB (exercises the existing happy path) with explicit config
     values: embedding_model=bge-m3, chunk_size=768, top_k=8,
     score_threshold=0.35, retrieval_mode=keyword
  2. GET config → 200, all 6 fields echo the CreateKB values;
     ocr_enabled defaults to false (007 migration column default)
  3. DB-level truth: the raw knowledge_bases row really holds the values
     (incl. ocr_enabled column presence — proves migration 007 applied)
  4. Negative: GET config on missing KB → 404
  5. Negative: GET config with a kb_id belonging to ANOTHER tenant → 404
     (RLS tenant isolation through the whole chain)
  6. Negative: GET config on a soft-DELETED KB → 404 (get_kb filters
     status <> 'deleted'; same semantics as GetKB)
  7. Cleanup: direct purge of the KB row so the shared dev DB stays clean

Services (local only, NEVER uploaded to the server):
  - ani-gateway (Go, :8080)      repo/bin/ani-gateway.exe
  - kb-service  (Python, gRPC :50053)

Infrastructure (server-deployed NodePorts on 10.10.1.66):
  PostgreSQL :30945  (ocr_enabled applied via migration 007)

Usage:
  python tests/e2e/test_kb_config_e2e.py
"""
from __future__ import annotations

import io
import json
import os
import subprocess
import sys
import time
import urllib.request
import urllib.error
import uuid
from datetime import datetime
from pathlib import Path
from typing import Any

# ── paths ────────────────────────────────────────────────────────────────────
REPO = Path(__file__).resolve().parents[4]  # repo/
KB_SVC = REPO / "services" / "kb-service"
GATEWAY_EXE = REPO / "bin" / "ani-gateway.exe"
E2E_LOG = KB_SVC / "tests" / "e2e" / "e2e_kb_config_result.log"

sys.path.insert(0, str(KB_SVC))

# ── server component endpoints (NodePorts on 10.10.1.66) ─────────────────────
SRV = "10.10.1.66"
PG_URL = f"postgres://ani_app_user:ani_dev_password@{SRV}:30945/ani?sslmode=disable"

TENANT_ID = "00000000-0000-0000-0000-000000000002"
OTHER_TENANT_ID = "00000000-0000-0000-0000-000000000003"
E2E_TAG = f"kbcfg-{int(time.time())}"

REST_BASE = "http://localhost:8080/api/v1/svc"
REST_HDRS = {"Content-Type": "application/json", "X-Dev-Tenant-ID": TENANT_ID}
OTHER_HDRS = {"Content-Type": "application/json", "X-Dev-Tenant-ID": OTHER_TENANT_ID}

# explicit config values used to seed the KB (all non-default on purpose
# so a "default leak" bug cannot masquerade as a pass)
CFG = {
    "embedding_model": "bge-m3",
    "chunk_size": 768,
    "top_k": 8,
    "score_threshold": 0.35,
    "retrieval_mode": "keyword",
}

# ── logging (terminal + file) ────────────────────────────────────────────────
_log_fh: io.TextIOBase | None = None


def _open_log():
    global _log_fh
    E2E_LOG.parent.mkdir(parents=True, exist_ok=True)
    _log_fh = open(E2E_LOG, "w", encoding="utf-8")


def log(msg: str = "", **kw):
    ts = datetime.now().strftime("%H:%M:%S")
    line = f"[{ts}] {msg}" if msg else ""
    print(line, flush=True, **kw)
    if _log_fh:
        _log_fh.write(line + "\n")
        _log_fh.flush()


def log_json(label: str, data):
    log(f"── {label} ──")
    try:
        text = json.dumps(data, ensure_ascii=False, indent=2, default=str)
    except Exception:
        text = str(data)
    print(text, flush=True)
    if _log_fh:
        _log_fh.write(text + "\n")
        _log_fh.flush()


# ── process management ───────────────────────────────────────────────────────
_procs: list[subprocess.Popen] = []


def _start(args: list[str], env: dict, cwd: Path, name: str) -> subprocess.Popen:
    full_env = os.environ.copy()
    full_env.update(env)
    log(f"Starting {name}: {' '.join(args[:3])}... (cwd={cwd})")
    log_file = open(E2E_LOG.parent / f"{name}_kbcfg.stdout.log", "w",
                    encoding="utf-8")
    p = subprocess.Popen(
        args,
        env=full_env,
        cwd=str(cwd),
        stdout=log_file,
        stderr=subprocess.STDOUT,
        creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == "nt" else 0,
    )
    _procs.append(p)
    log(f"  {name} started: pid={p.pid}")
    return p


def _wait_http(url: str, timeout: float = 60.0) -> bool:
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            urllib.request.urlopen(url, timeout=3)
            log(f"  ready at {url}")
            return True
        except urllib.error.HTTPError:
            log(f"  ready (HTTP error response = server up) at {url}")
            return True
        except Exception:
            time.sleep(1.0)
    log(f"  NOT ready at {url} (timeout {timeout}s)")
    return False


def _kill_all():
    for p in reversed(_procs):
        try:
            p.terminate()
            p.wait(timeout=5)
        except Exception:
            try:
                p.kill()
            except Exception:
                pass


def _kill_stale_processes():
    """Kill any previously-started local service processes on our ports."""
    for n in ("ani-gateway", "kb-service", "uvicorn"):
        try:
            subprocess.run(
                ["taskkill", "/F", "/IM", f"{n}.exe"],
                capture_output=True, timeout=10,
            )
        except Exception:
            pass
    for port in (8002, 50053, 8080):
        try:
            out = subprocess.run(
                ["netstat", "-ano", "-p", "tcp"],
                capture_output=True, text=True, timeout=10,
            ).stdout
            for line in out.splitlines():
                if f":{port}" in line and "LISTENING" in line:
                    pid = line.strip().split()[-1]
                    subprocess.run(["taskkill", "/F", "/PID", pid],
                                   capture_output=True, timeout=10)
        except Exception:
            pass
    time.sleep(1.0)


# ── service environments ─────────────────────────────────────────────────────
def _gateway_env():
    env = os.environ.copy()
    env.update({
        "ANI_AUTH_MODE": "dev",
        "GATEWAY_LISTEN_ADDR": ":8080",
        "DATABASE_URL": PG_URL,
        "REDIS_URL": f"redis://:ani_dev_password@{SRV}:30453/0",
        # pgvector/object/vector components are not needed for the
        # config read flow, but the gateway refuses to start without a
        # database; wire the server endpoints (same as the B4 e2e)
        "OBJECT_STORE_PROVIDER": "minio",
        "OBJECT_STORE_ENDPOINT": f"{SRV}:30900",
        "OBJECT_STORE_PUBLIC_ENDPOINT": f"{SRV}:30900",
        "OBJECT_STORE_ACCESS_KEY_ID": "ani-s05-minio",
        "OBJECT_STORE_SECRET_ACCESS_KEY": "F36UCbnRR-bY9Upv8uuammuBwkHFlTYABiXCbtMCmlc",
        "OBJECT_STORE_SECURE": "false",
        "OBJECT_STORE_BUCKET_PREFIX": "ani-s13-",
        "OBJECT_STORE_REGION": "us-east-1",
        "VECTOR_STORE_PROVIDER": "milvus",
        "VECTOR_STORE_ENDPOINT": f"http://{SRV}:31930",
        "VECTOR_STORE_COLLECTION_PREFIX": "ani_s13_",
        "NATS_URL": f"nats://{SRV}:31062",
        "STORAGE_PROVIDER": "",
        "K8S_CLUSTER_PROVIDER_MODE": "",
        "GPU_INVENTORY_PROVIDER": "",
        "NETWORK_PROVIDER": "",
        "REGISTRY_PROVIDER_MODE": "",
        "INSTANCE_OBSERVABILITY_PROVIDER": "",
        "KB_SERVICE_GRPC_ADDR": "localhost:50053",
        "KB_SERVICE_GRPC_CALL_TIMEOUT": "30s",
    })
    return env


def _kb_service_env():
    env = os.environ.copy()
    env.update({
        "DATABASE_URL": PG_URL,
        "NATS_URL": f"nats://{SRV}:31062",
        # unique subjects so the server-deployed consumers never race us
        "NATS_PARSE_SUBJECT": f"ani.tasks.kb.parse.kbcfg.{E2E_TAG}",
        "NATS_PARSE_SUBJECT_V2": f"ani.tasks.kb.parse.kbcfg.v2.{E2E_TAG}",
        "ANI_GATEWAY_INTERNAL_URL": "http://localhost:8080",
        "GRPC_PORT": "50053",
        "REDIS_URL": f"redis://:ani_dev_password@{SRV}:30453/0",
        # parse consumer OFF: no doc upload in this flow; the outbox
        # dispatcher tolerates events queuing unpublished
        "KB_PARSE_CONSUMER_ENABLED": "false",
        "ANI_DEV_TENANT_ID": TENANT_ID,
    })
    return env


def start_gateway():
    _start([str(GATEWAY_EXE)], _gateway_env(),
           REPO / "services" / "ani-gateway", "gateway")


def start_kb_service():
    _start([sys.executable, "main.py"], _kb_service_env(), KB_SVC, "kb-service")


# ── REST helpers ─────────────────────────────────────────────────────────────
RESULTS: dict[str, str] = {}


def check(label: str, ok: bool, detail: str = ""):
    RESULTS[label] = "pass" if ok else "fail"
    mark = "✓" if ok else "✗"
    log(f"  {mark} {label}" + (f" — {detail}" if detail else ""))


def rest_request(method: str, path: str, body: dict | None = None,
                 expected_status: int | None = None, label: str = "",
                 timeout: float = 60.0, base: str | None = None,
                 headers: dict | None = None, desc: str = "",
                 ) -> tuple[int, Any]:
    """Send a REST request through the gateway; log purpose/input/output."""
    url = (base or REST_BASE) + path
    hdrs = dict(headers or REST_HDRS)
    data = None
    if body is not None:
        data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        hdrs.setdefault("Content-Type", "application/json")
    if desc:
        log(f"[{label or method}] 接口作用: {desc}")
    log(f"[{label or method + ' ' + path}] INPUT: {method} {url}")
    if body is not None:
        log_json(f"[{label}] INPUT BODY", body)
    status = -1
    payload: Any = None
    try:
        req = urllib.request.Request(url, data=data, method=method.upper(),
                                     headers=hdrs)
        resp = urllib.request.urlopen(req, timeout=timeout)
        status = resp.status
        raw = resp.read().decode("utf-8", errors="replace")
        if raw:
            try:
                payload = json.loads(raw)
            except json.JSONDecodeError:
                payload = raw
    except urllib.error.HTTPError as e:
        status = e.code
        raw = e.read().decode("utf-8", errors="replace") if e.fp else ""
        if raw:
            try:
                payload = json.loads(raw)
            except json.JSONDecodeError:
                payload = raw
    except Exception as e:
        log(f"[{label}] REQUEST ERROR: {e!r}")
        return -1, {"error": repr(e)}
    log(f"[{label}] OUTPUT: HTTP {status}"
        + (f" (期望 {expected_status})" if expected_status is not None else ""))
    log_json(f"[{label}] OUTPUT BODY", payload)
    if expected_status is not None and status != expected_status:
        log(f"[{label}] ✗ UNEXPECTED STATUS: expected={expected_status} actual={status}")
    return status, payload


# ── PG helpers (cleanup + raw-row verification) ───────────────────────────────
def _run_async(coro) -> Any:
    import asyncio
    return asyncio.run(coro)


# SSH + kubectl exec channel into the server postgres pod — the same
# channel used for migration 007. Needed because the NodePort RLS user
# ani_app_user has no DELETE privilege on async_tasks (its purge
# transaction would roll back entirely, leaving the KB row behind).
SSH = ["ssh", "-o", "BatchMode=yes", f"kubercloud@{SRV}"]
PSQL = ("kubectl exec -i -n ani-system ani-reconcile-ha-postgres-0 -- "
        "psql -U ani -d ani -v ON_ERROR_STOP=1")


def _purge_kb(kb_id: str, idem_key: str):
    """Physically purge this run's KB row (DeleteKB is a SOFT delete;
    pure e2e test data must not linger in the shared dev DB)."""
    sql = f"""
        DELETE FROM kb_documents WHERE kb_id = '{kb_id}';
        DELETE FROM knowledge_bases WHERE id = '{kb_id}';
        DELETE FROM async_tasks
         WHERE tenant_id = '{TENANT_ID}'
           AND (resource_id = '{kb_id}'::uuid OR idempotency_key = '{idem_key}');
        UPDATE outbox_events
           SET published = TRUE, published_at = now()
         WHERE tenant_id = '{TENANT_ID}'
           AND payload->>'kb_id' = '{kb_id}'
           AND published = FALSE;
    """
    r = subprocess.run(SSH + [PSQL], input=sql, capture_output=True,
                       text=True, timeout=60)
    if r.returncode != 0:
        raise RuntimeError(f"purge via kubectl psql failed: {r.stderr}")
    log(f"  purged KB {kb_id} (via kubectl psql)")
    log(f"  psql output: {r.stdout.strip()}")


async def _read_kb_row(kb_id: str) -> dict | None:
    """Read the raw knowledge_bases row (verify the CreateKB config values
    really persisted, incl. the ocr_enabled column from migration 007)."""
    import asyncpg
    conn = await asyncpg.connect(PG_URL)
    try:
        async with conn.transaction():
            await conn.execute(
                f"SET LOCAL app.current_tenant_id = '{TENANT_ID}'")
            row = await conn.fetchrow(
                """
                SELECT embedding_model, chunk_size, ocr_enabled,
                       top_k, score_threshold, retrieval_mode
                  FROM knowledge_bases WHERE id=$1
                """,
                uuid.UUID(kb_id))
            if row is None:
                return None
            return {
                "embedding_model": row["embedding_model"],
                "chunk_size": row["chunk_size"],
                "ocr_enabled": row["ocr_enabled"],
                "top_k": row["top_k"],
                "score_threshold": row["score_threshold"],
                "retrieval_mode": row["retrieval_mode"],
            }
    finally:
        await conn.close()


# ── E2E flow ─────────────────────────────────────────────────────────────────
def run_e2e() -> int:
    # 1. CreateKB — real KB via the REST chain with explicit config values
    kb_name = f"kbcfg-e2e-{E2E_TAG}"
    idem_key = str(uuid.uuid4())
    status, kb = rest_request(
        "POST", "/knowledge-bases", body={
            "idempotency_key": idem_key,
            "name": kb_name,
            "description": "B5 #22 config e2e test KB",
            **CFG,
        },
        expected_status=201, label="CreateKB",
        desc="创建测试用知识库（config 读取测试的前置数据，全部使用非默认配置值）",
        timeout=120)
    check("CreateKB -> 201", status == 201, f"status={status}")
    if status != 201 or not isinstance(kb, dict):
        return 1
    kb_id = kb.get("id", "")
    check("CreateKB body has id", bool(kb_id), f"id={kb_id}")
    if not kb_id:
        return 1

    # 2. GET config → 200, all 6 fields echo the CreateKB values
    status, cfg = rest_request(
        "GET", f"/knowledge-bases/{kb_id}/config",
        expected_status=200, label="GET config",
        desc="读取知识库配置：入库区（embedding_model/chunk_size/ocr_enabled）+ 问答区"
             "（top_k/score_threshold/retrieval_mode），6 字段应回显创建值",
        timeout=30)
    check("GET config -> 200", status == 200, f"status={status}")
    if status == 200 and isinstance(cfg, dict):
        check("embedding_model == bge-m3",
              cfg.get("embedding_model") == CFG["embedding_model"],
              f"embedding_model={cfg.get('embedding_model')!r}")
        check("chunk_size == 768",
              cfg.get("chunk_size") == CFG["chunk_size"],
              f"chunk_size={cfg.get('chunk_size')!r}")
        check("ocr_enabled == false (column default)",
              cfg.get("ocr_enabled") is False,
              f"ocr_enabled={cfg.get('ocr_enabled')!r}")
        check("top_k == 8",
              cfg.get("top_k") == CFG["top_k"],
              f"top_k={cfg.get('top_k')!r}")
        check("score_threshold == 0.35",
              cfg.get("score_threshold") == CFG["score_threshold"],
              f"score_threshold={cfg.get('score_threshold')!r}")
        check("retrieval_mode == keyword",
              cfg.get("retrieval_mode") == CFG["retrieval_mode"],
              f"retrieval_mode={cfg.get('retrieval_mode')!r}")
        # response must be exactly the 6 config fields — no extras leaked
        check("body has exactly the 6 contract fields",
              sorted(cfg.keys()) == sorted([
                  "embedding_model", "chunk_size", "ocr_enabled",
                  "top_k", "score_threshold", "retrieval_mode"]),
              f"keys={sorted(cfg.keys())}")

    # 3. DB-level truth: the raw row really holds the values
    #    (incl. ocr_enabled column presence — proves migration 007 applied)
    row = _run_async(_read_kb_row(kb_id))
    log_json("DB knowledge_bases row (config columns)", row)
    # score_threshold is REAL (float4) in PG: 0.35 reads back as
    # 0.3499999940395355 in a raw-row read — compare with tolerance
    st_ok = (row is not None
             and abs(row["score_threshold"] - CFG["score_threshold"]) < 1e-6)
    check("DB row persisted (all 6 config values)",
          row is not None
          and row["embedding_model"] == CFG["embedding_model"]
          and row["chunk_size"] == CFG["chunk_size"]
          and row["ocr_enabled"] is False
          and row["top_k"] == CFG["top_k"]
          and st_ok
          and row["retrieval_mode"] == CFG["retrieval_mode"],
          f"row={row}")

    # 4. Negative: GET config on missing KB → 404
    status, err = rest_request(
        "GET", "/knowledge-bases/00000000-0000-0000-0000-999999999999/config",
        expected_status=404, label="GET missing KB",
        desc="负例：不存在的 KB → 404 NOT_FOUND（走 servicer abort NOT_FOUND → "
             "gateway mapGRPCError 映射）",
        timeout=30)
    check("GET missing KB -> 404", status == 404, f"status={status}")

    # 5. Negative: cross-tenant access → 404 (RLS isolation, whole chain)
    status, err = rest_request(
        "GET", f"/knowledge-bases/{kb_id}/config",
        expected_status=404, label="GET cross-tenant",
        headers=OTHER_HDRS,
        desc="负例：另一租户读本租户的 KB 配置 → 404（RLS 租户隔离贯穿整条链路）",
        timeout=30)
    check("GET cross-tenant -> 404", status == 404, f"status={status}")

    # 6. Negative: soft-DELETED KB → 404 (get_kb filters status <> 'deleted')
    status, _ = rest_request(
        "DELETE", f"/knowledge-bases/{kb_id}",
        expected_status=204, label="DeleteKB",
        desc="软删测试 KB：删除后 status='deleted'，config 读路径应不可见（404）",
        timeout=60)
    check("DeleteKB -> 204", status == 204, f"status={status}")
    status, err = rest_request(
        "GET", f"/knowledge-bases/{kb_id}/config",
        expected_status=404, label="GET after soft-delete",
        desc="负例：软删后的 KB → 404（get_kb 的 status <> 'deleted' 过滤，"
             "与 GetKB/GetKBPermissions 同语义）",
        timeout=30)
    check("GET after soft-delete -> 404", status == 404, f"status={status}")

    # 7. Cleanup: direct physical purge
    try:
        _purge_kb(kb_id, idem_key)
    except Exception as e:
        log(f"  purge failed (acceptable — rows may cascade): {e!r}")

    return 0


# ── main ─────────────────────────────────────────────────────────────────────
def main():
    _open_log()
    log("════════════════════════════════════════════════════════════════")
    log(f"  B5 #22 config E2E：GET /knowledge-bases/{{kb_id}}/config — tag={E2E_TAG}")
    log(f"  tenant={TENANT_ID}")
    log(f"  log file: {E2E_LOG}")
    log("════════════════════════════════════════════════════════════════")

    if not GATEWAY_EXE.exists():
        log(f"FATAL: gateway exe not found at {GATEWAY_EXE} — run go build first")
        return 1

    log("── Killing stale processes on ports 8080/8002/50053 ──")
    _kill_stale_processes()

    try:
        # 1. start kb-service (gRPC :50053; FastAPI :8002 also comes up)
        log("── Starting kb-service (gRPC :50053) ──")
        start_kb_service()
        if not _wait_http("http://localhost:8002/health", 90):
            log("WARNING: kb-service /health not ready — continuing")

        import socket
        ok = False
        deadline = time.time() + 60
        while time.time() < deadline:
            try:
                with socket.create_connection(("localhost", 50053), timeout=2):
                    ok = True
                    break
            except Exception:
                time.sleep(1)
        log(f"  kb-service gRPC :50053 {'ready' if ok else 'NOT reachable'}")
        if not ok:
            log("FATAL: kb-service gRPC not reachable on :50053")
            return 1

        # 2. start gateway
        log("── Starting ani-gateway (:8080) ──")
        start_gateway()
        if not _wait_http(
                "http://localhost:8080/api/v1/svc/knowledge-bases?limit=1", 60):
            log("FATAL: gateway not ready")
            return 1

        # warm up gateway->kb-service gRPC channel
        deadline = time.time() + 30
        warmed = False
        while time.time() < deadline:
            status, _ = rest_request(
                "GET", "/knowledge-bases?limit=1", label="grpc-channel warmup",
                desc="通道预热: 轮询直到网关→kb-service gRPC 通道就绪（非 503）",
                timeout=10)
            if status != 503:
                warmed = True
                break
            time.sleep(2)
        if not warmed:
            log("FATAL: gateway→kb-service gRPC channel still 503 after 30s")
            return 1
        log("  gateway→kb-service gRPC channel warm")

        # 3. run E2E
        rc = run_e2e()

        # 4. summary
        n_pass = sum(1 for v in RESULTS.values() if v == "pass")
        n_fail = sum(1 for v in RESULTS.values() if v == "fail")
        log("══════════════════════════ RESULT SUMMARY ══════════════════════════")
        log(f"  PASS: {n_pass}   FAIL: {n_fail}   total: {len(RESULTS)}")
        log("  接口覆盖: #22 GET config（6 字段回显/契约字段精确/DB 真值/"
            "404 missing/RLS 跨租户 404/软删 404）")
        if n_fail:
            log("  FAILED checks:")
            for k, v in RESULTS.items():
                if v == "fail":
                    log(f"    ✗ {k}")
        log(f"  full log: {E2E_LOG}")
        log("════════════════════════════════════════════════════════════════")
        return rc if rc else (0 if n_fail == 0 else 2)
    finally:
        _kill_all()
        log("── All services stopped ──")


if __name__ == "__main__":
    sys.exit(main())
