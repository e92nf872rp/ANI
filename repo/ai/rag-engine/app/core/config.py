from pydantic import AliasChoices, Field, ValidationInfo, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        # The ANI monorepo .env is shared across services; ignore vars we don't use.
        extra="ignore",
    )

    # Full Milvus address ``host:port``; takes precedence over the separate
    # fields below when set (matches the ``MILVUS_ADDR`` env var in .env).
    milvus_addr: str = ""
    milvus_host: str = "localhost"
    milvus_port: int = 19530
    # Embedding model served by the AI inference service (OpenAI compatible
    # /v1/embeddings). US-013: rag-engine calls the remote endpoint instead of
    # loading a local HuggingFace model. ``embedding_model`` is the model name
    # passed to the remote service (e.g. ``Qwen3-Embedding-0.6B``);
    # ``embedding_api_base`` is the OpenAI-compatible base URL; the temporary
    # default points to the interim embedding service and will be replaced by
    # the formal inference-service address once it deploys an embedding model.
    embedding_model: str = "Qwen3-Embedding-0.6B"
    embedding_api_base: str = "http://10.10.20.197:8006/v1"
    # API key for the remote embedding service. Empty means no auth (the
    # interim service has no api_key); the formal inference-service may set one.
    embedding_api_key: str = ""
    embedding_dim: int = 1024
    # Redis for ChatMemoryBuffer(RedisChatStore) — qa_service multi-turn session
    # memory (SPEC §5.1 qa_service). Defaults to the dev Redis in .env.
    redis_url: str = "redis://localhost:6379/0"
    # PostgreSQL DSN for kb_chunks table (pg_trgm keyword retrieval + parent
    # backfill + chunk writes). SPEC §2.4 requires config.py to include PG
    # configuration. Used by make_pg_trgm_search_fn / make_parent_lookup_fn /
    # chunks repository (all accept a DSN string or asyncpg pool).
    # #5: Accept both PG_DSN (legacy) and DATABASE_URL (shared .env convention
    # used by kb-service) via validation_alias.
    pg_dsn: str = Field(
        default="postgresql://ani:ani_dev_password@localhost:5432/ani",
        validation_alias=AliasChoices("pg_dsn", "database_url", "DATABASE_URL", "PG_DSN"),
    )
    # NATS for parse_worker subscription (SPEC §2.4, §5.1 parse_worker).
    # parse_worker subscribes to ani.tasks.kb.parse (US-015).
    nats_url: str = "nats://localhost:4222"
    nats_parse_subject: str = "ani.tasks.kb.parse"
    # LLM served by the AI inference service (OpenAI-compatible /v1). US-012
    # summary_service calls this endpoint for document-level summarization.
    # The AI inference service exposes the OpenAI interface to the knowledge
    # base module; rag-engine does NOT load a local LLM. The defaults below
    # point at an interim LLM endpoint and are overridden by .env (VLLM_*).
    # Replace VLLM_API_BASE / VLLM_MODEL once the formal inference-service
    # deploys an LLM model.
    vllm_model: str = ""
    vllm_api_base: str = ""
    vllm_api_key: str = ""
    vllm_context_window: int = 32768
    # Internal ANI Gateway address for token validation.
    # #5: Accept both ANI_GATEWAY_URL (legacy) and ANI_GATEWAY_INTERNAL_URL
    # (shared .env convention used by kb-service).
    ani_gateway_url: str = Field(
        default="http://ani-gateway.ani-system.svc.cluster.local:8080",
        validation_alias=AliasChoices(
            "ani_gateway_url", "ani_gateway_internal_url",
            "ANI_GATEWAY_URL", "ANI_GATEWAY_INTERNAL_URL",
        ),
    )
    # AI service OCR API base URL (PaddleOCR PP-OCRv4, deployed by inference-service, issue #5).
    ocr_api_base: str = "http://inference-service.ani-system.svc.cluster.local:8000"
    ocr_timeout_seconds: float = 30.0
    # MinIO endpoint for image upload during document parsing.
    minio_endpoint: str = "minio.ani-system.svc.cluster.local:9000"
    minio_access_key: str = ""
    minio_secret_key: str = ""
    minio_secure: bool = False
    minio_bucket: str = "ani-kb-docs"

    @field_validator("milvus_host", mode="after")
    @classmethod
    def _apply_milvus_addr(cls, v: str, info: ValidationInfo) -> str:
        addr = info.data.get("milvus_addr")
        if addr and ":" in addr:
            host, _ = addr.rsplit(":", 1)
            return host
        return v

    @field_validator("milvus_port", mode="after")
    @classmethod
    def _apply_milvus_port(cls, v: int, info: ValidationInfo) -> int:
        addr = info.data.get("milvus_addr")
        if addr and ":" in addr:
            _, port = addr.rsplit(":", 1)
            try:
                return int(port)
            except ValueError:
                return v
        return v


settings = Settings()
