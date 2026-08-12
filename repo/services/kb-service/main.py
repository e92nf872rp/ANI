"""ANI kb-service entrypoint (SPEC §2.4).

Starts the KBService gRPC server on the configured port. FastAPI is exposed
alongside for health/readiness; business RPCs are served over gRPC.

US-010 wires the async DB pool, the outbox dispatcher (polls outbox_events and
publishes to NATS `ani.tasks.kb.parse`), and the Redis session cache factory.

Startup order:
  1. start the dedicated gRPC event loop on a background thread
  2. build the asyncpg pool **on the gRPC loop** (asyncpg connections bind to
     the event loop they were created on; gRPC servicer methods run on
     ThreadPoolExecutor threads and submit coroutines to the gRPC loop via
     run_coroutine_threadsafe, so the pool must live on that same loop)
  3. build a second asyncpg pool on the uvicorn loop for the outbox dispatcher
     (the dispatcher runs as a background task on the uvicorn loop)
  4. connect NATS (best-effort; service still starts if NATS is down)
  5. start the outbox dispatcher coroutine on the uvicorn loop
  6. start the gRPC server in a background thread, passing the gRPC-loop pool
     to the servicer so DB-backed RPCs work in production
"""
import asyncio
import logging
import os
import sys
from concurrent import futures
from contextlib import asynccontextmanager

import asyncpg
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
from app.generated.kb.v1 import kb_service_pb2_grpc as pb_grpc

logger = logging.getLogger(__name__)

# Process-global resources, initialized in the lifespan and referenced by
# /readyz so health reflects real component availability.
_db_pool: asyncpg.Pool | None = None        # gRPC-loop pool (servicer)
_outbox_pool: asyncpg.Pool | None = None    # uvicorn-loop pool (outbox dispatcher)
_outbox_dispatcher = None
_nats_client = None
_session_cache = None
_grpc_server: grpc.Server | None = None


async def _build_pool() -> asyncpg.Pool:
    """Build the asyncpg pool for the outbox dispatcher (uvicorn loop)."""
    return await asyncpg.create_pool(dsn=settings.database_url, min_size=1, max_size=5)


def _build_grpc_pool() -> asyncpg.Pool:
    """Build the asyncpg pool on the dedicated gRPC event loop.

    asyncpg connections bind to the event loop they were created on. gRPC
    servicer methods run on ThreadPoolExecutor threads and submit coroutines
    to the gRPC loop via run_coroutine_threadsafe, so the pool must be created
    on that same loop to avoid "Future attached to a different loop" errors.
    """
    import asyncpg as _asyncpg

    async def _create():
        return await _asyncpg.create_pool(
            dsn=settings.database_url, min_size=2, max_size=10
        )

    loop = _grpc_server_module._grpc_loop
    future = asyncio.run_coroutine_threadsafe(_create(), loop)
    return future.result()


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


def _start_grpc_server(pool: asyncpg.Pool, session_cache=None) -> grpc.Server:
    """Start the gRPC server (blocking call done in a background thread).

    The pool must be created on the dedicated gRPC event loop (see
    _build_grpc_pool) so asyncpg connections don't cross event loops.
    """
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    pb_grpc.add_KBServiceServicer_to_server(
        KBServiceServicer(pool=pool, session_cache_factory=lambda: session_cache),
        server,
    )
    server.add_insecure_port(f"[::]:{settings.grpc_port}")
    server.start()
    return server


@asynccontextmanager
async def lifespan(app: FastAPI):
    """Manage DB pools + NATS + outbox dispatcher + gRPC server lifecycle."""
    global _db_pool, _outbox_pool, _outbox_dispatcher, _nats_client, _session_cache, _grpc_server

    # 1. start the dedicated gRPC event loop on a background thread
    _start_grpc_loop()

    # 2. build the asyncpg pool on the gRPC loop (for gRPC servicer RPCs)
    _db_pool = _build_grpc_pool()

    # 3. build a separate asyncpg pool on the uvicorn loop (for outbox dispatcher)
    _outbox_pool = await _build_pool()

    # 4. connect NATS (best-effort)
    _nats_client = await _build_nats_client()
    _session_cache = _build_session_cache()
    if _nats_client is not None:
        from app.outbox.dispatcher import OutboxDispatcher

        _outbox_dispatcher = OutboxDispatcher(
            pool=_outbox_pool,
            nats_client=_nats_client,
            subject=settings.nats_parse_subject,
        )
        _outbox_dispatcher.start()
        logger.info("outbox dispatcher started (subject=%s)", settings.nats_parse_subject)
    # 6. start the gRPC server in a background thread with the gRPC-loop pool
    _grpc_server = _start_grpc_server(_db_pool, session_cache=_session_cache)
    print(f"kb-service gRPC server listening on :{settings.grpc_port}", flush=True)
    yield
    if _grpc_server is not None:
        _grpc_server.stop(grace=5)
    if _outbox_dispatcher is not None:
        await _outbox_dispatcher.stop()
    if _nats_client is not None:
        await _nats_client.drain()
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
    # Close the outbox pool (uvicorn loop)
    if _outbox_pool is not None:
        await _outbox_pool.close()
    # Close the gRPC-loop pool on its own loop
    if _db_pool is not None:
        loop = _grpc_server_module._grpc_loop
        future = asyncio.run_coroutine_threadsafe(_db_pool.close(), loop)
        try:
            future.result(timeout=5)
        except Exception:  # noqa: BLE001 — best-effort cleanup
            pass


app = FastAPI(title="ANI kb-service", version="1.0.0", lifespan=lifespan)


@app.get("/health")
async def health():
    return {"status": "ok"}


@app.get("/readyz")
async def readyz():
    # US-010: readiness reflects DB pools + outbox dispatcher + cache availability.
    # session_cache is best-effort (Query degrades to DB-only), so it does not
    # gate readiness — reported for observability but not required for "ok".
    ready = {
        "db": _db_pool is not None and not _db_pool._closed,
        "outbox_db": _outbox_pool is not None and not _outbox_pool._closed,
        "outbox_dispatcher": _outbox_dispatcher is not None,
        "session_cache": _session_cache is not None,
        "grpc": _grpc_server is not None,
    }
    # Cache is best-effort: only db + outbox + grpc gate the "ok" status.
    ok = ready["db"] and ready["outbox_dispatcher"] and ready["grpc"]
    return {"status": "ok" if ok else "degraded", "components": ready}


def main():
    # uvicorn owns the event loop and runs the lifespan (startup + shutdown).
    # The gRPC server is started inside the lifespan on a background thread.
    uvicorn.run(app, host="0.0.0.0", port=8002, log_level="info")


if __name__ == "__main__":
    main()
