from pydantic import AliasChoices, Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        # The ANI monorepo .env is shared across services; ignore vars we don't use.
        extra="ignore",
    )

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
    # LLM served by the AI inference service (OpenAI-compatible /v1). US-0012
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
    # gRPC server bind address (Plan §2.3: stateless gRPC engine).
    # Override via GRPC_BIND_ADDR env var.
    grpc_bind_addr: str = "[::]:50052"


settings = Settings()
