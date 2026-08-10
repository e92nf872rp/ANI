"""Fake rag-engine — returns canned sources for SSE E2E test.

Stands in for the real rag-engine whose Milvus connection has an event-loop
issue in the local dev environment. Returns a fixed SourceChunk list so the
gateway SSE handler can exercise the full token→sources→done sequence.
"""
import json
from fastapi import FastAPI, Request
import uvicorn

app = FastAPI(title="fake-rag-engine")


@app.get("/health")
async def health():
    return {"status": "ok", "grpc_server": True, "parse_worker": False, "db_pool": True}


@app.post("/api/v1/kb/{kb_id}/query")
async def query_kb(kb_id: str, req: Request):
    body = await req.json()
    return {
        "answer": "This is a canned answer from fake rag-engine.",
        "sources": [
            {
                "doc_id": "doc-fake-001",
                "file_name": "fake-document.pdf",
                "page": 1,
                "content": "This is fake retrieved content for testing the SSE endpoint.",
                "score": 0.95,
            },
            {
                "doc_id": "doc-fake-002",
                "file_name": "another-doc.txt",
                "page": 3,
                "content": "More fake content for the SSE end-to-end test.",
                "score": 0.82,
            },
        ],
        "session_id": "fake-session-001",
        "input_tokens": 50,
        "output_tokens": 30,
    }


if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=8005)
