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

    # Redis (session cache) — maps to env REDIS_URL
    redis_url: str = "redis://localhost:6379/0"

    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
    )

    @property
    def core_api_base_url(self) -> str:
        return f"{self.ani_gateway_internal_url.rstrip('/')}{self.core_api_base_path}"


settings = Settings()
