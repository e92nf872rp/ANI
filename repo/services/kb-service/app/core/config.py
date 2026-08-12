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

    # PostgreSQL — maps to env DATABASE_URL.
    # issue-031: kb-service no longer connects to PostgreSQL directly (no
    # asyncpg). DATABASE_URL is retained for the Core data plane / managed
    # migrations; kb-service accesses data via CoreClient.data_query.
    database_url: str = "postgresql://ani:ani@localhost:5432/ani"

    # Core OpenAPI REST base (vector-stores / objects).
    # Derived from ANI_GATEWAY_INTERNAL_URL (gateway host) + /api/v1 path.
    ani_gateway_internal_url: str = "http://ani-gateway.ani-system.svc.cluster.local:8080"
    core_api_base_path: str = "/api/v1"

    # rag-engine REST (Query). kb-service calls rag-engine's Query RPC over
    # REST (POST /api/v1/kb/{kb_id}/query), so this must point at the
    # rag-engine HTTP server (spec §2.1). Default: rag-engine REST on 8001.
    rag_engine_addr: str = "localhost:8001"

    # NATS (outbox dispatch) — maps to env NATS_URL
    nats_url: str = "nats://localhost:4222"
    nats_parse_subject: str = "ani.tasks.kb.parse"

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
