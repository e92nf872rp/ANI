"""kb-service configuration (SPEC §2.4).

Loads from the shared repo-root `.env` (see .env.example). Environment
variable names are case-insensitive (pydantic-settings), so these fields
map to DATABASE_URL / NATS_URL / REDIS_URL / ANI_GATEWAY_INTERNAL_URL as
defined in the project .env. Extra env vars from other services are
ignored — the `.env` is shared across ANI services.
"""
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    # gRPC server
    grpc_port: int = 50053

    # PostgreSQL (asyncpg) — maps to env DATABASE_URL
    database_url: str = "postgresql://ani:ani@localhost:5432/ani"

    # Core OpenAPI REST base (vector-stores / objects).
    # Derived from ANI_GATEWAY_INTERNAL_URL (gateway host) + /api/v1 path.
    ani_gateway_internal_url: str = "http://ani-gateway.ani-system.svc.cluster.local:8080"
    core_api_base_path: str = "/api/v1"

    # rag-engine gRPC (Parse/Embed/Generate). Plan §3.4: the stateless
    # RPCs are accessed via gRPC (default localhost:50052).
    rag_engine_grpc_addr: str = "localhost:50052"

    # NATS (outbox dispatch) — maps to env NATS_URL
    nats_url: str = "nats://localhost:4222"
    nats_parse_subject: str = "ani.tasks.kb.parse"
    # Plan §0.3 / step 6: new v2 subject consumed by the kb-service
    # NATS consumer (app/consumers/parse_consumer.py). The legacy subject
    # ``nats_parse_subject`` is kept unchanged for the rag-engine parse_worker
    # path; the Outbox Dispatcher switches between the two via
    # ``kb_parse_consumer_enabled``.
    nats_parse_subject_v2: str = "ani.tasks.kb.parse.v2"

    # Plan step 6: kb-service NATS consumer flag (default OFF).
    # When False, the consumer does not start and the Outbox Dispatcher
    # publishes to the legacy subject (rag-engine parse_worker path).
    # When True, the consumer starts and the Outbox Dispatcher publishes
    # to ``nats_parse_subject_v2`` (kb-service consumer path).
    kb_parse_consumer_enabled: bool = False

    # P1 #24 full-KB rebuild: dedicated subject for kb.rebuild outbox
    # events (OutboxDispatcher routes event_type 'kb.rebuild' here via
    # subject_overrides), consumed by app/consumers/rebuild_consumer.py.
    # Distinct from the parse subjects: rebuild is a long serial job and
    # must not interleave with per-document parse traffic.
    nats_rebuild_subject: str = "ani.tasks.kb.rebuild.v1"

    # P1 #24: kb-service rebuild consumer flag (default OFF, mirrors
    # kb_parse_consumer_enabled rollout). When False the consumer does
    # not start; rebuild outbox events stay queued until the flag is
    # enabled (at-least-once via the outbox, no loss).
    kb_rebuild_consumer_enabled: bool = False

    # Redis (session cache) — maps to env REDIS_URL
    redis_url: str = "redis://localhost:6379/0"

    # Embedding model defaults — maps to the shared env EMBEDDING_MODEL /
    # EMBEDDING_DIM (same keys rag-engine reads; pydantic-settings is
    # case-insensitive). Used by CreateKB as the fallback when the request
    # omits embedding_model, and as the Core vector-store dimension. The
    # default value mirrors the shared .env EMBEDDING_MODEL (SiliconFlow
    # requires the full prefixed name "BAAI/bge-m3"; the bare alias is NOT
    # recognised by the remote endpoint).
    embedding_model: str = "BAAI/bge-m3"
    embedding_dim: int = 1024

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
    )

    @property
    def core_api_base_url(self) -> str:
        return f"{self.ani_gateway_internal_url.rstrip('/')}{self.core_api_base_path}"


settings = Settings()
