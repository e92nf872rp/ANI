# Files

- [Document Parser (Python - Planned)](doc-parser.md) - Python document parser service (ai/doc-parser/): planned. CI build scaffold exists in build-image.yml referencing the directory context, but no code directory on disk yet. Intended for Docling-based parsing with PaddleOCR integration.
- [RAG Engine (Python)](rag-engine.md) - Python RAG engine (ai/rag-engine/): FastAPI-based microservice with gRPC server. Milvus vector store for document retrieval. Text2vec embeddings. Document chunking/indexing. Query/QA service with SSE streaming. NATS worker for async parsing.
- [Whisper Speech-to-Text (Python - Prototype)](whisper-service.md) - Python Faster-Whisper speech-to-text service: prototype/placeholder — not yet implemented, no code directory on disk. CI build scaffold does not exist yet.
