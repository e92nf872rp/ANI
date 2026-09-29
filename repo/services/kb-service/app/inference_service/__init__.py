"""kb-service inference-service client package.

Hosts ``InferenceServiceGRPCClient``, the internal-API client used to
resolve a published ``served_model_name`` to its cluster runtime endpoint
(``ResolveInferenceServiceEndpoint``). The endpoint is never surfaced to
tenants — it is passed straight through to rag-engine.
"""
