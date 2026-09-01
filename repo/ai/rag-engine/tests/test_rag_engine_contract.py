"""rag-engine gRPC contract tests (issue-034 / Plan Â§7.3).

Verifies the gRPC contract surface of rag-engine's stateless RPCs:

  - Parse RPC contract: download_url (not bytes) â€?the request accepts a
    URL string, not raw document bytes.
  - Embed RPC contract: vectors_flat + dimension + count â€?the response
    uses a flattened 1-D array with explicit dimension and count fields.
  - Generate RPC contract: history includes current-turn user + question
    appended at end (reproduces {query_str}); system prompt uses
    DEFAULT_CONTEXT_TEMPLATE + DEFAULT_REFINE_TEMPLATE; context truncation
    reproduces CompactAndRefine.
  - SourceChunk contract: chunk_id field exists (proto field 1).

These are contract tests, not unit tests â€?they verify the proto message
shape and gRPC servicer contract surface, not the internal implementation
details. They use the same stubs as other rag-engine tests (conftest.py).
"""
from __future__ import annotations

import sys
from unittest.mock import MagicMock

import pytest
from app.grpc import rag_pb2 as rag_pb
from app.grpc.server import RagEngineServicer
from app.services.generate_rpc_service import (
    GenerateRPCService,
)


class FakeContext:
    """gRPC servicer context fake (records abort calls)."""

    def __init__(self) -> None:
        self.aborted_code = None
        self.aborted_details = None

    async def abort(self, code, details):
        self.aborted_code = code
        self.aborted_details = details
        raise Exception(f"aborted: {code} {details}")  # noqa: TRY002


# â”€â”€ Contract test class (Plan Â§7.3) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


class TestRagEngineContract:
    """Contract tests for rag-engine gRPC RPCs (Plan Â§7.3).

    Each test verifies a specific contract property of the proto messages
    or the gRPC servicer, ensuring the wire format matches what kb-service
    expects.
    """

    # â”€â”€ Parse RPC contract: download_url (not bytes) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

    @pytest.mark.asyncio
    async def test_parse_rpc_contract(self):
        """Parse RPC contract: request uses download_url (string), not bytes.

        The ParseRequest proto must have a ``download_url`` field (string)
        and must NOT have a ``document_bytes`` or similar bytes field. This
        ensures kb-service passes a presigned URL (avoiding the gRPC 4MB
        limit) rather than raw document bytes.
        """
        # Verify ParseRequest can be constructed with download_url (string)
        req = rag_pb.ParseRequest(download_url="http://example.com/doc.pdf")
        assert req.download_url == "http://example.com/doc.pdf"

        # Verify ParseRequest has file_name, file_type, chunk_size fields
        req = rag_pb.ParseRequest(
            download_url="http://example.com/doc.pdf",
            file_name="doc.pdf",
            file_type="pdf",
            chunk_size=1024,
        )
        assert req.file_name == "doc.pdf"
        assert req.file_type == "pdf"
        assert req.chunk_size == 1024

        # Verify ParseRequest does NOT accept document_bytes (bytes field)
        # If a bytes field existed, we could set it; verify it doesn't
        try:
            rag_pb.ParseRequest(document_bytes=b"raw content")
            # If no exception, the field exists â€?contract violation
            pytest.fail("ParseRequest should NOT have a document_bytes field")
        except (TypeError, ValueError):
            pass  # Expected: no such field

        # Verify the servicer rejects empty download_url
        servicer = RagEngineServicer()
        ctx = FakeContext()
        req = rag_pb.ParseRequest(download_url="", file_name="test.txt")
        with pytest.raises(Exception):  # noqa: B017
            await servicer.Parse(req, ctx)
        assert ctx.aborted_code is not None

    # â”€â”€ Embed RPC contract: vectors_flat + dimension + count â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

    @pytest.mark.asyncio
    async def test_embed_rpc_contract(self, monkeypatch):
        """Embed RPC contract: response uses vectors_flat (1-D array) +
        dimension + count.

        The EmbedResponse proto must have:
          - ``vectors_flat``: repeated float (flattened 1-D array)
          - ``dimension``: int32 (embedding dimension)
          - ``count``: int32 (number of vectors)

        The servicer must return vectors_flat where
        vectors_flat[i * dimension + j] = j-th dim of i-th text.
        """
        # Verify EmbedResponse can be constructed with all contract fields
        resp = rag_pb.EmbedResponse(
            vectors_flat=[1.0, 2.0, 3.0, 4.0, 5.0, 6.0],
            dimension=3,
            count=2,
        )
        assert list(resp.vectors_flat) == [1.0, 2.0, 3.0, 4.0, 5.0, 6.0]
        assert resp.dimension == 3
        assert resp.count == 2
        # Contract: vectors_flat length = count * dimension
        assert len(resp.vectors_flat) == resp.count * resp.dimension

        # Verify the servicer returns the correct flattened structure
        fake_model = MagicMock()
        fake_model.get_text_embedding_batch.return_value = [
            [1.0, 2.0, 3.0],
            [4.0, 5.0, 6.0],
        ]

        monkeypatch.setattr(
            "app.services.embed_rpc_service.get_embed_model",
            lambda: fake_model,
        )

        servicer = RagEngineServicer()
        ctx = FakeContext()
        req = rag_pb.EmbedRequest(texts=["a", "b"])
        resp = await servicer.Embed(req, ctx)

        # Contract: vectors_flat = [1,2,3,4,5,6], dimension=3, count=2
        assert list(resp.vectors_flat) == [1.0, 2.0, 3.0, 4.0, 5.0, 6.0]
        assert resp.dimension == 3
        assert resp.count == 2
        assert len(resp.vectors_flat) == resp.count * resp.dimension

    # â”€â”€ Generate RPC contract: history + question + templates + truncation â”€

    @pytest.mark.asyncio
    async def test_generate_rpc_contract(self, monkeypatch):
        """Generate RPC contract: history includes current-turn user +
        question appended at end; system prompt uses
        DEFAULT_CONTEXT_TEMPLATE + DEFAULT_REFINE_TEMPLATE; context
        truncation reproduces CompactAndRefine.

        Verifies:
        1. GenerateRequest has ``question``, ``history``, ``context``,
           ``session_id``, ``max_tokens``, ``inference_service_name``.
        2. GenerateResponse has ``answer``, ``input_tokens``,
           ``output_tokens``, ``session_id``.
        3. The servicer maps proto messages to the GenerateRPCService with
           history (including current-turn user) + question appended.
        """
        # â”€â”€ 1. Verify GenerateRequest proto fields by construction â”€â”€
        req = rag_pb.GenerateRequest(
            question="what is RAG?",
            session_id="sess-1",
            context=[rag_pb.SourceChunk(content="RAG context", chunk_id="c1")],
            history=[
                rag_pb.ChatMessage(role="user", content="what is RAG?"),
            ],
            inference_service_name="default",
            max_tokens=1024,
        )
        assert req.question == "what is RAG?"
        assert req.session_id == "sess-1"
        assert req.inference_service_name == "default"
        assert req.max_tokens == 1024
        assert len(req.context) == 1
        assert req.context[0].content == "RAG context"
        assert len(req.history) == 1
        assert req.history[0].role == "user"

        # â”€â”€ 2. Verify GenerateResponse proto fields by construction â”€â”€
        resp = rag_pb.GenerateResponse(
            answer="answer text",
            input_tokens=10,
            output_tokens=5,
            session_id="sess-1",
        )
        assert resp.answer == "answer text"
        assert resp.input_tokens == 10
        assert resp.output_tokens == 5
        assert resp.session_id == "sess-1"

        # â”€â”€ 3. Verify servicer maps history + question correctly â”€â”€
        captured: list[dict] = []

        class _FakeCompletions:
            def create(self, **kwargs):
                captured.append(kwargs)
                r = MagicMock()
                r.choices = [MagicMock()]
                r.choices[0].message.content = "answer"
                r.usage = MagicMock()
                r.usage.prompt_tokens = 10
                r.usage.completion_tokens = 5
                return r

        class _FakeChat:
            def __init__(self):
                self.completions = _FakeCompletions()

        class _FakeOpenAI:
            def __init__(self, **kwargs):
                self.chat = _FakeChat()

        openai_mod = sys.modules.get("openai")
        monkeypatch.setattr(openai_mod, "OpenAI", _FakeOpenAI)

        servicer = RagEngineServicer()
        ctx = FakeContext()
        req = rag_pb.GenerateRequest(
            question="what is RAG?",
            session_id="sess-1",
            context=[rag_pb.SourceChunk(content="RAG context", chunk_id="c1")],
            history=[
                rag_pb.ChatMessage(role="user", content="what is RAG?"),
            ],
            max_tokens=1024,
        )
        resp = await servicer.Generate(req, ctx)

        assert ctx.aborted_code is None
        assert resp.answer == "answer"
        assert resp.input_tokens == 10
        assert resp.output_tokens == 5
        assert resp.session_id == "sess-1"

        # Contract: messages = [SYSTEM + history + USER:question]
        assert len(captured) == 1
        messages = captured[0]["messages"]
        # SYSTEM + 1 history + 1 question = 3
        assert len(messages) == 3
        assert messages[0]["role"] == "system"
        # Last message is USER: question (reproduces {query_str})
        assert messages[-1]["role"] == "user"
        assert messages[-1]["content"] == "what is RAG?"
        # History includes current-turn user (appears twice)
        user_msgs = [
            m for m in messages
            if m["role"] == "user" and m["content"] == "what is RAG?"
        ]
        assert len(user_msgs) == 2

    @pytest.mark.asyncio
    async def test_generate_rpc_contract_system_prompt_templates(self, monkeypatch):
        """Generate RPC contract: system prompt uses DEFAULT_CONTEXT_TEMPLATE
        for the first round and DEFAULT_REFINE_TEMPLATE for subsequent rounds.

        This verifies the CompactAndRefine reproduction:
        - First call: SYSTEM = DEFAULT_CONTEXT_TEMPLATE.format(context_str=segment)
        - Second call: SYSTEM = DEFAULT_REFINE_TEMPLATE.format(
            context_msg=segment, existing_answer=prev_answer)
        """
        from app.core.config import settings

        settings.vllm_context_window = 4096

        captured_systems: list[str] = []
        call_count = [0]

        class _FakeCompletions:
            def create(self, **kwargs):
                call_count[0] += 1
                captured_systems.append(kwargs["messages"][0]["content"])
                r = MagicMock()
                r.choices = [MagicMock()]
                r.usage = MagicMock()
                if call_count[0] == 1:
                    r.choices[0].message.content = "initial answer"
                    r.usage.prompt_tokens = 5
                    r.usage.completion_tokens = 3
                else:
                    r.choices[0].message.content = "refined answer"
                    r.usage.prompt_tokens = 7
                    r.usage.completion_tokens = 4
                return r

        class _FakeChat:
            def __init__(self):
                self.completions = _FakeCompletions()

        class _FakeOpenAI:
            def __init__(self, **kwargs):
                self.chat = _FakeChat()

        openai_mod = sys.modules.get("openai")
        monkeypatch.setattr(openai_mod, "OpenAI", _FakeOpenAI)

        svc = GenerateRPCService()
        long_text = "x" * 20000
        result = svc.generate("q", "s", [{"content": long_text}], [])

        # Contract: multiple rounds (CompactAndRefine)
        assert call_count[0] > 1
        # First round: DEFAULT_CONTEXT_TEMPLATE
        assert "Use the context information below" in captured_systems[0]
        assert "{context_str}" not in captured_systems[0]
        # Second round: DEFAULT_REFINE_TEMPLATE
        assert "refine the following existing answer" in captured_systems[1]
        assert "initial answer" in captured_systems[1]
        # Final answer from last refine round
        assert result["answer"] == "refined answer"

    @pytest.mark.asyncio
    async def test_generate_rpc_contract_context_truncation(self, monkeypatch):
        """Generate RPC contract: context truncation reproduces
        CompactAndRefine repack.

        Context longer than max_context_chars is split into segments. Each
        segment fits within the LLM context window. The number of LLM calls
        equals the number of segments.
        """
        from app.core.config import settings

        settings.vllm_context_window = 4096

        call_count = [0]

        class _FakeCompletions:
            def create(self, **kwargs):
                call_count[0] += 1
                r = MagicMock()
                r.choices = [MagicMock()]
                r.choices[0].message.content = "answer"
                r.usage = MagicMock()
                r.usage.prompt_tokens = 1
                r.usage.completion_tokens = 1
                return r

        class _FakeChat:
            def __init__(self):
                self.completions = _FakeCompletions()

        class _FakeOpenAI:
            def __init__(self, **kwargs):
                self.chat = _FakeChat()

        openai_mod = sys.modules.get("openai")
        monkeypatch.setattr(openai_mod, "OpenAI", _FakeOpenAI)

        svc = GenerateRPCService()
        # Context that fits in a single segment
        svc.generate("q", "s", [{"content": "short"}], [])
        assert call_count[0] == 1  # single call

        # Context that exceeds single segment â†?multiple calls
        call_count[0] = 0
        long_text = "x" * 20000
        svc.generate("q", "s", [{"content": long_text}], [])
        assert call_count[0] > 1  # multiple calls (CompactAndRefine)

    # â”€â”€ SourceChunk contract: chunk_id field exists â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€

    def test_source_chunk_has_chunk_id(self):
        """SourceChunk proto contract: chunk_id field exists (field 1).

        The SourceChunk message must have a ``chunk_id`` field as field
        number 1 (for RRF rank-fusion key). This is the contract that
        kb-service's RetrieveService relies on to identify chunks across
        vector and keyword retrieval legs.
        """
        # Verify SourceChunk can be constructed with chunk_id
        chunk = rag_pb.SourceChunk(chunk_id="c1", content="text", score=0.9)
        assert chunk.chunk_id == "c1"
        assert chunk.content == "text"
        assert chunk.score == pytest.approx(0.9)

        # Verify other SourceChunk fields (renumbered per rag.proto)
        chunk_full = rag_pb.SourceChunk(
            chunk_id="c1",
            doc_id="d1",
            file_name="a.pdf",
            page=3,
            content="chunk content",
            score=0.85,
        )
        assert chunk_full.chunk_id == "c1"
        assert chunk_full.doc_id == "d1"
        assert chunk_full.file_name == "a.pdf"
        assert chunk_full.page == 3
        assert chunk_full.content == "chunk content"
        assert chunk_full.score == pytest.approx(0.85)

        # Verify chunk_id defaults to empty string (backwards compatible)
        empty_chunk = rag_pb.SourceChunk()
        assert empty_chunk.chunk_id == ""


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
