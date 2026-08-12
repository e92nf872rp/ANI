"""ANI kb-service entrypoint (SPEC §2.4).

Starts the KBService gRPC server on the configured port. FastAPI is exposed
alongside for health/readiness; business RPCs are served over gRPC.

issue-031: kb-service no longer owns any asyncpg pool. All data access goes
through the Core data plane via CoreClient (SPEC §4.3). `pool` on
KBServiceServicer is a non-None sentinel that gates DB-backed RPCs; the outbox
dispatcher receives a CoreClient instance (role="service") for cross-tenant
outbox scanning (SPEC §4.2).

Startup order:
  1. start the dedicated gRPC event loop on a background thread
  2. build a CoreClient for the outbox dispatcher (uvicorn loop)
  3. connect NATS (best-effort; service still starts if NATS is down)
  4. start the outbox dispatcher coroutine on the uvicorn loop
  5. start the gRPC server in a background thread, passing a non-None pool
     sentinel + the core_client_factory to the servicer
"""
import asyncio
import logging
import os
import sys
from concurrent import futures
from contextlib import asynccontextmanager

import grpc
import uvicorn
from fastapi import FastAPI

# Make both the kb-service package root (for `app.*` imports) and the
# generated stubs root (for top-level `common.v1` / `kb.v1` imports used by
# the protoc-generated grpc code) importable regardless of CWD.
_HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, _HERE)                       # so `import app...` works
sys.path.insert(0, os.path.join(_HERE, "app", "generated"))  # so `import common.v1` / `kb.v1` works

from app.api import grpc_server as _grpc_server_module
from app.api.grpc_server import KBServiceServicer, _start_grpc_loop
from app.core.config import settings
from app.core_api.client import CoreClient
from app.generated.kb.v1 import kb_service_pb2_grpc as pb_grpc

logger = logging.getLogger(__name__)

# Process-global resources, initialized in the lifespan and referenced by
# /readyz so health reflects real component availability.
# issue-031: no asyncpg pools — _pool_sentinel is a non-None object that gates
# DB-backed RPCs on the servicer (config sentinel, not a connection pool).
# _outbox_core is the CoreClient used by the outbox dispatcher (data plane).
# _core_client_factory caches per-tenant CoreClients so the httpx connection
# pool is reused across RPCs instead of being created+destroyed per request
# (performance fix for the per-RPC httpx client issue).
_pool_sentinel = object()
_outbox_core: CoreClient | None = None      # CoreClient for outbox dispatcher
_core_client_factory = None  # _CoreClientFactory instance for servicer (set in lifespan)
_outbox_dispatcher = None
_nats_client = None
_session_cache = None
_grpc_server: grpc.Server | None = None


class _CoreClientFactory:
    """Caches per-tenant CoreClients so httpx connections are reused.

    Each tenant gets its own CoreClient (own httpx.AsyncClient + connection
    pool + X-Tenant-Id header) so RLS tenant isolation is preserved. The
    client is cached across RPCs so the httpx connection pool survives
    between requests instead of being created and destroyed per `async with`.

    The cached CoreClient's `_owns_client` is set to False so that
    `async with factory(tenant_id) as core:` (used by the servicer) does NOT
    close the client on block exit — the client is closed only at process
    shutdown via `aclose()`.
    """

    def __init__(self, *, base_url: str, dev_scope: str | None = None) -> None:
        self._base_url = base_url
        self._dev_scope = dev_scope
        self._clients: dict[str, CoreClient] = {}

    def __call__(self, tenant_id: str) -> CoreClient:
        cc = self._clients.get(tenant_id)
        if cc is None:
            extra_headers: dict[str, str] = {}
            if self._dev_scope:
                extra_headers["X-Dev-Scope"] = self._dev_scope
            cc = CoreClient(
                base_url=self._base_url,
                tenant_id=tenant_id,
                extra_headers=extra_headers or None,
            )
            # Prevent __aexit__ from closing the cached client per-RPC.
            cc._owns_client = False
            self._clients[tenant_id] = cc
        return cc

    async def aclose(self) -> None:
        """Close all cached CoreClients at process shutdown."""
        for cc in self._clients.values():
            # Force-close the underlying httpx client regardless of
            # _owns_client (which we set to False to prevent per-RPC close).
            try:
                await cc._client.aclose()
            except Exception:  # noqa: BLE001 — best-effort cleanup
                pass
        self._clients.clear()


def _build_outbox_core_client() -> CoreClient:
    """Build a CoreClient for the outbox dispatcher (cross-tenant, service role).

    The dispatcher uses role="service" for cross-tenant outbox scanning
    (SPEC §4.2). The CoreClient tenant_id is set to a placeholder — the
    dispatcher's data_query calls explicitly pass role="service" which
    bypasses RLS (SPEC §4.2).
    """
    extra_headers: dict[str, str] = {}
    auth_mode = os.environ.get("ANI_AUTH_MODE", "").lower()
    if auth_mode == "dev":
        # role=service is cross-tenant and requires platform scope (SPEC §3.3-7).
        # The gateway's data-plane handler rejects role=service when scope != "platform".
        extra_headers["X-Dev-Scope"] = "platform"
    return CoreClient(
        base_url=settings.core_api_base_url,
        tenant_id="00000000-0000-0000-0000-000000000000",  # placeholder; service role bypasses RLS
        extra_headers=extra_headers or None,
    )


async def _build_nats_client():
    """Connect to NATS for the outbox dispatcher; return None on failure.

    The outbox dispatcher publishes parse tasks to NATS. If NATS is
    unavailable at startup, the service still starts: events accumulate in
    outbox_events and are dispatched once NATS recovers (SPEC §7.3).
    """
    try:
        from nats import connect as nats_connect

        nc = await nats_connect(settings.nats_url, name="kb-service-outbox")
        return nc
    except Exception as e:  # noqa: BLE001 — NATS is best-effort at startup
        logger.warning("NATS connect failed; outbox will retry via dispatcher: %s", e)
        return None


def _build_session_cache():
    """Build a singleton SessionCache from settings; None if Redis unavailable.

    Query degrades to DB-only persistence when Redis is down (SPEC §7.3).
    Built once at startup so each Query RPC reuses the same connection pool
    instead of constructing a new Redis client per call.

    The Redis client is created on the dedicated gRPC event loop because
    redis.asyncio connections bind to the event loop they were created on,
    and SessionCache.append_message is called from the gRPC servicer (which
    runs on the gRPC loop).

    Note: aioredis.from_url() and SessionCache.__init__ do NOT open connections
    (lazy connect on first command), so a construction failure here does not
    leak a connection pool — no explicit close is needed in the error path.
    """
    try:
        import redis.asyncio as aioredis

        from app.session.cache import SessionCache

        async def _create():
            client = aioredis.from_url(settings.redis_url, decode_responses=False)
            return SessionCache(redis=client)

        loop = _grpc_server_module._grpc_loop
        future = asyncio.run_coroutine_threadsafe(_create(), loop)
        return future.result()
    except Exception as e:  # noqa: BLE001 — best-effort cache wiring
        logger.warning("Redis session cache unavailable (Query will be DB-only): %s", e)
        return None


def _start_grpc_server(
    *,
    session_cache=None,
    core_client_factory: _CoreClientFactory | None = None,
) -> grpc.Server:
    """Start the gRPC server (blocking call done in a background thread).

    issue-031: the servicer's pool parameter is a non-None sentinel — actual
    data access goes through core_client_factory (CoreClient data plane).
    The injected factory caches per-tenant CoreClients so httpx connections
    are reused across RPCs; when None, the servicer falls back to the
    per-RPC _default_core_client (used by tests).
    """
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    pb_grpc.add_KBServiceServicer_to_server(
        KBServiceServicer(
            pool=_pool_sentinel,
            session_cache_factory=lambda: session_cache,
            core_client_factory=core_client_factory,
        ),
        server,
    )
    server.add_insecure_port(f"[::]:{settings.grpc_port}")
    server.start()
    return server


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Manage CoreClient + NATS + outbox dispatcher + gRPC server lifecycle."""
    global _outbox_core, _core_client_factory, _outbox_dispatcher, _nats_client, _session_cache, _grpc_server

    # 1. start the dedicated gRPC event loop on a background thread
    _start_grpc_loop()

    # 2. build the CoreClient for the outbox dispatcher (uvicorn loop)
    _outbox_core = _build_outbox_core_client()

    # 2b. build the shared per-tenant CoreClient factory for the gRPC servicer
    # so httpx connections are reused across RPCs instead of created+destroyed
    # per request.
    auth_mode = os.environ.get("ANI_AUTH_MODE", "").lower()
    _core_client_factory = _CoreClientFactory(
        base_url=settings.core_api_base_url,
        dev_scope="service" if auth_mode == "dev" else None,
    )

    # 3. connect NATS (best-effort). The dispatcher is always started — when
    # NATS is unavailable at startup it self-heals by lazily reconnecting via
    # the nats_connect callable (SPEC §7.3: delayed dispatch, not lost work).
    _nats_client = await _build_nats_client()
    _session_cache = _build_session_cache()
    from app.outbox.dispatcher import OutboxDispatcher

    async def _nats_connect():
        """Reconnect callable for the dispatcher's lazy NATS recovery."""
        return await _build_nats_client()

    _outbox_dispatcher = OutboxDispatcher(
        core_client=_outbox_core,
        nats_client=_nats_client,
        subject=settings.nats_parse_subject,
        nats_connect=_nats_connect,
    )
    _outbox_dispatcher.start()
    logger.info(
        "outbox dispatcher started (subject=%s, nats=%s)",
        settings.nats_parse_subject,
        "connected" if _nats_client is not None else "pending-reconnect",
    )
    # 4. start the gRPC server in a background thread
    _grpc_server = _start_grpc_server(
        session_cache=_session_cache,
        core_client_factory=_core_client_factory,
    )
    print(f"kb-service gRPC server listening on :{settings.grpc_port}", flush=True)
    yield
    if _grpc_server is not None:
        _grpc_server.stop(grace=5)
    if _outbox_dispatcher is not None:
        await _outbox_dispatcher.stop()
    # The dispatcher owns NATS lifecycle when it may have (re)connected; only
    # drain the startup client if the dispatcher never took ownership (i.e.
    # the dispatcher wasn't started or its current NATS client isn't the
    # startup client).
    if (
        _nats_client is not None
        and (
            _outbox_dispatcher is None
            or _outbox_dispatcher.nats_client is not _nats_client
        )
    ):
        try:
            await _nats_client.drain()
        except Exception:  # noqa: BLE001 — best-effort cleanup
            pass
    if _session_cache is not None:
        # Close the Redis connection pool backing the session cache on the
        # gRPC loop (same loop where the Redis client was created).
        loop = _grpc_server_module._grpc_loop
        future = asyncio.run_coroutine_threadsafe(
            _session_cache._redis.aclose(), loop
        )
        try:
            future.result(timeout=5)
        except Exception:  # noqa: BLE001 — best-effort cleanup
            pass
    # Close the shared per-tenant CoreClients (servicer path)
    if _core_client_factory is not None:
        await _core_client_factory.aclose()
    # Close the outbox CoreClient
    if _outbox_core is not None:
        await _outbox_core.aclose()


app = FastAPI(title="ANI kb-service", version="1.0.0", lifespan=lifespan)


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/readyz")
async def readyz():
    # issue-031: readiness reflects data-plane reachability (SPEC §4.3).
    # The former db/outbox_db asyncpg pool probes are replaced by an actual
    # data-plane ping: _outbox_core is the CoreClient used by the outbox
    # dispatcher, and we issue a lightweight GET /healthz to verify the
    # gateway is actually reachable (not just that a CoreClient was
    # constructed, which is always true after lifespan startup).
    # The pool sentinel is always set (non-None) once the servicer is
    # constructed, so it doesn't gate readiness.
    data_plane_reachable = False
    if _outbox_core is not None:
        data_plane_reachable = await _outbox_core.ping()
    ready = {
        "data_plane": data_plane_reachable,
        "outbox_dispatcher": _outbox_dispatcher is not None,
        "session_cache": _session_cache is not None,
        "grpc": _grpc_server is not None,
    }
    # Cache is best-effort: only data_plane + outbox + grpc gate "ok".
    ok = ready["data_plane"] and ready["outbox_dispatcher"] and ready["grpc"]
    return {"status": "ok" if ok else "degraded", "components": ready}


def main():
    # uvicorn owns the event loop and runs the lifespan (startup + shutdown).
    # The gRPC server is started inside the lifespan on a background thread.
    uvicorn.run(app, host="0.0.0.0", port=8002, log_level="info")


if __name__ == "__main__":
    main()
