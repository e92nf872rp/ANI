"""Tests for Generate RPC (RAG-REFACTOR-STEP-2 / Plan Â§2.4).

Validates:
* history includes current-turn user + question appended at end (reproduces
  {query_str} template â€?user appears twice).
* system prompt = DEFAULT_CONTEXT_TEMPLATE.
* refine template = DEFAULT_REFINE_TEMPLATE.
* context truncation reproduces CompactAndRefine repack.
* multi-round CompactAndRefine: first segment â†?QA, subsequent â†?refine.
* token accumulation across refine rounds.
* timeout â†?DEADLINE_EXCEEDED.
* response.usage token extraction.
* GenerateStream event sequence (token* â†?done).

Stubs in conftest.py.
"""
from __future__ import annotations

import sys
from unittest.mock import MagicMock

import grpc
import pytest
from app.grpc import rag_pb2 as rag_pb
from app.grpc.server import RagEngineServicer
from app.services.generate_rpc_service import (
    DEFAULT_CONTEXT_TEMPLATE,
    DEFAULT_REFINE_TEMPLATE,
    GenerateRPCService,
    _repack_context,
)


class FakeContext:
    def __init__(self) -> None:
        self.aborted_code = None
        self.aborted_details = None

    async def abort(self, code, details):
        self.aborted_code = code
        self.aborted_details = details
        raise Exception(f"aborted: {code} {details}")  # noqa: TRY002


# â”€â”€ Template reproduction (Plan Â§2.4) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_default_context_template_matches_llamaindex():
    """DEFAULT_CONTEXT_TEMPLATE reproduces LlamaIndex's default."""
    expected = (
        "Use the context information below to assist the user."
        "\n--------------------\n"
        "{context_str}"
        "\n--------------------\n"
    )
    assert DEFAULT_CONTEXT_TEMPLATE == expected


def test_default_refine_template_matches_llamaindex():
    """DEFAULT_REFINE_TEMPLATE reproduces LlamaIndex's default."""
    expected = (
        "Using the context below, refine the following existing answer"
        " using the provided context to assist the user."
        "\nIf the context isn't helpful, just repeat the existing answer"
        " and nothing more."
        "\n--------------------\n"
        "{context_msg}"
        "\n--------------------\n"
        "Existing Answer:\n"
        "{existing_answer}"
        "\n--------------------\n"
    )
    assert DEFAULT_REFINE_TEMPLATE == expected


# â”€â”€ _build_messages: history + question duplication â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_build_messages_includes_history_and_appends_question():
    """history contains current-turn user; question appended as final USER.

    This reproduces the legacy {query_str} behavior: the current-turn user
    message appears twice (once in history, once as the final USER message).
    """
    svc = GenerateRPCService()
    history = [
        {"role": "user", "content": "first question"},
        {"role": "assistant", "content": "first answer"},
        {"role": "user", "content": "second question"},  # current-turn user
    ]
    messages = svc._build_messages("second question", [], history)
    # SYSTEM + 3 history + 1 question = 5
    assert len(messages) == 5
    assert messages[0]["role"] == "system"
    # Last message is USER: question (reproduces {query_str} template)
    assert messages[-1]["role"] == "user"
    assert messages[-1]["content"] == "second question"
    # Current-turn user also in history (appears twice)
    user_msgs = [m for m in messages if m["role"] == "user" and m["content"] == "second question"]
    assert len(user_msgs) == 2


def test_build_messages_empty_history():
    """Empty history â†?just SYSTEM + USER: question."""
    svc = GenerateRPCService()
    messages = svc._build_messages("test question", [], [])
    assert len(messages) == 2
    assert messages[0]["role"] == "system"
    assert messages[1]["role"] == "user"
    assert messages[1]["content"] == "test question"


def test_build_messages_system_prompt_uses_context_template():
    """SYSTEM message = DEFAULT_CONTEXT_TEMPLATE.format(context_str=...)."""
    svc = GenerateRPCService()
    context = [{"content": "chunk1"}, {"content": "chunk2"}]
    messages = svc._build_messages("q", context, [])
    expected_context = "chunk1\n\nchunk2"
    expected_system = DEFAULT_CONTEXT_TEMPLATE.format(context_str=expected_context)
    assert messages[0]["content"] == expected_system


# â”€â”€ Context truncation (CompactAndRefine repack) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_build_context_str_empty():
    svc = GenerateRPCService()
    assert svc._build_context_str([]) == ""


def test_build_context_str_joins_with_double_newline():
    svc = GenerateRPCService()
    context = [{"content": "a"}, {"content": "b"}]
    assert svc._build_context_str(context) == "a\n\nb"


def test_build_context_str_truncates_long_context(monkeypatch):
    """Context longer than max_context_chars is truncated."""
    from app.core.config import settings

    monkeypatch.setattr(settings, "vllm_context_window", 4096)
    svc = GenerateRPCService()
    # max_context_chars = (4096 - 2048 - 200) * 4 = 7392
    long_text = "x" * 10000
    context = [{"content": long_text}]
    result = svc._build_context_str(context)
    assert len(result) <= 7392
    assert result == long_text[:7392]


def test_build_context_str_short_context_not_truncated():
    svc = GenerateRPCService()
    short = "short context"
    context = [{"content": short}]
    assert svc._build_context_str(context) == short


# â”€â”€ _repack_context (CompactAndRefine segment splitting) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_repack_context_empty():
    """Empty context â†?empty segments list."""
    assert _repack_context([], 100) == []


def test_repack_context_empty_content():
    """Context with empty content strings â†?empty segments list."""
    assert _repack_context([{"content": ""}, {"content": ""}], 100) == []


def test_repack_context_single_segment():
    """Context fitting within max â†?single segment."""
    context = [{"content": "a"}, {"content": "b"}]
    segments = _repack_context(context, 100)
    assert len(segments) == 1
    assert segments[0] == "a\n\nb"


def test_repack_context_multiple_segments():
    """Context exceeding max â†?split into multiple segments."""
    long_text = "x" * 100
    context = [{"content": long_text}]
    segments = _repack_context(context, 30)
    # 100 chars / 30 per segment = 4 segments (30+30+30+10)
    assert len(segments) == 4
    assert len(segments[0]) == 30
    assert len(segments[1]) == 30
    assert len(segments[2]) == 30
    assert len(segments[3]) == 10
    # Verify total content preserved
    assert "".join(segments) == long_text


def test_repack_context_joins_before_splitting():
    """Multiple chunks are joined with \\n\\n before splitting."""
    context = [{"content": "aaa"}, {"content": "bbb"}]
    segments = _repack_context(context, 10)
    # "aaa\n\nbbb" = 9 chars, fits in 10
    assert len(segments) == 1
    assert segments[0] == "aaa\n\nbbb"


# â”€â”€ _build_refine_messages â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_build_refine_messages_uses_refine_template():
    """Refine round SYSTEM message = DEFAULT_REFINE_TEMPLATE.format(...)."""
    svc = GenerateRPCService()
    history = [{"role": "user", "content": "q"}]
    messages = svc._build_refine_messages(
        question="q",
        context_msg="new context",
        existing_answer="previous answer",
        history=history,
    )
    # SYSTEM + 1 history + 1 question = 3
    assert len(messages) == 3
    assert messages[0]["role"] == "system"
    expected_system = DEFAULT_REFINE_TEMPLATE.format(
        context_msg="new context",
        existing_answer="previous answer",
    )
    assert messages[0]["content"] == expected_system
    # History and question still appended
    assert messages[1]["role"] == "user"
    assert messages[1]["content"] == "q"
    assert messages[2]["role"] == "user"
    assert messages[2]["content"] == "q"  # question duplicated (query_str)


def test_build_refine_messages_empty_history():
    """Refine round with empty history â†?SYSTEM + USER: question."""
    svc = GenerateRPCService()
    messages = svc._build_refine_messages("q", "ctx", "prev", [])
    assert len(messages) == 2
    assert messages[0]["role"] == "system"
    assert DEFAULT_REFINE_TEMPLATE.split("{")[0] in messages[0]["content"]


# â”€â”€ Generate (non-streaming, single round) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def _make_fake_response(answer="test answer", input_tokens=10, output_tokens=20):
    """Build a fake openai chat completion response."""
    resp = MagicMock()
    resp.choices = [MagicMock()]
    resp.choices[0].message.content = answer
    resp.usage = MagicMock()
    resp.usage.prompt_tokens = input_tokens
    resp.usage.completion_tokens = output_tokens
    return resp


def _patch_openai(monkeypatch, completions_cls=None):
    """Patch openai.OpenAI with a fake client. Returns the fake class."""
    if completions_cls is None:
        completions_cls = type("_C", (), {"create": lambda self, **kw: _make_fake_response()})

    class _FakeChat:
        def __init__(self):
            self.completions = completions_cls()

    class _FakeOpenAI:
        def __init__(self, **kwargs):
            self.chat = _FakeChat()

    monkeypatch.setattr(sys.modules["openai"], "OpenAI", _FakeOpenAI)
    return _FakeOpenAI


def test_generate_calls_openai_sdk(monkeypatch):
    """Generate uses openai.OpenAI.chat.completions.create."""
    svc = GenerateRPCService()
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return _make_fake_response()

    _patch_openai(monkeypatch, _FakeCompletions)

    result = svc.generate(
        question="what is RAG?",
        session_id="s1",
        context=[{"content": "RAG is retrieval augmented generation."}],
        history=[{"role": "user", "content": "what is RAG?"}],
        max_tokens=2048,
    )
    assert result["answer"] == "test answer"
    assert result["input_tokens"] == 10
    assert result["output_tokens"] == 20
    assert result["session_id"] == "s1"
    # Single segment â†?single call
    assert len(captured) == 1
    # Messages: SYSTEM + 1 history + 1 question
    assert len(captured[0]["messages"]) == 3
    assert captured[0]["max_tokens"] == 2048


def test_generate_empty_answer(monkeypatch):
    """Empty answer from LLM â†?answer=""."""
    svc = GenerateRPCService()

    class _FakeCompletions:
        def create(self, **kwargs):
            return _make_fake_response(answer="")

    _patch_openai(monkeypatch, _FakeCompletions)
    result = svc.generate("q", "s", [], [])
    assert result["answer"] == ""


def test_generate_no_usage(monkeypatch):
    """No usage in response â†?tokens=0."""
    svc = GenerateRPCService()
    resp = MagicMock()
    resp.choices = [MagicMock()]
    resp.choices[0].message.content = "answer"
    resp.usage = None

    class _FakeCompletions:
        def create(self, **kwargs):
            return resp

    _patch_openai(monkeypatch, _FakeCompletions)
    result = svc.generate("q", "s", [], [])
    assert result["input_tokens"] == 0
    assert result["output_tokens"] == 0


def test_generate_timeout_error(monkeypatch):
    """Timeout from openai SDK â†?TimeoutError."""
    svc = GenerateRPCService()

    class _FakeCompletions:
        def create(self, **kwargs):
            raise TimeoutError("request timed out")

    _patch_openai(monkeypatch, _FakeCompletions)
    with pytest.raises(TimeoutError):
        svc.generate("q", "s", [], [])


def test_generate_connection_error(monkeypatch):
    """Connection error â†?RuntimeError."""
    svc = GenerateRPCService()

    class _FakeCompletions:
        def create(self, **kwargs):
            raise ConnectionError("connection refused")

    _patch_openai(monkeypatch, _FakeCompletions)
    with pytest.raises(RuntimeError, match="vLLM unavailable"):
        svc.generate("q", "s", [], [])


def test_generate_generic_api_error(monkeypatch):
    """Generic API error â†?RuntimeError."""
    svc = GenerateRPCService()

    class _FakeCompletions:
        def create(self, **kwargs):
            raise RuntimeError("internal server error")

    _patch_openai(monkeypatch, _FakeCompletions)
    with pytest.raises(RuntimeError, match="vLLM error"):
        svc.generate("q", "s", [], [])


# â”€â”€ Generate (multi-round CompactAndRefine) â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_generate_multi_round_uses_context_template_first(monkeypatch):
    """First round uses DEFAULT_CONTEXT_TEMPLATE, not refine template."""
    from app.core.config import settings
    monkeypatch.setattr(settings, "vllm_context_window", 4096)
    svc = GenerateRPCService()
    captured_systems = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured_systems.append(kwargs["messages"][0]["content"])
            return _make_fake_response(answer="answer")

    _patch_openai(monkeypatch, _FakeCompletions)

    # Context that fits in one segment
    svc.generate("q", "s", [{"content": "short context"}], [])
    assert len(captured_systems) == 1
    # First (only) call uses context template
    assert "{context_str}" not in captured_systems[0]  # template was formatted
    assert "Use the context information below" in captured_systems[0]


def test_generate_multi_round_uses_refine_template_second(monkeypatch):
    """Second round uses DEFAULT_REFINE_TEMPLATE with existing_answer."""
    from app.core.config import settings
    monkeypatch.setattr(settings, "vllm_context_window", 4096)
    svc = GenerateRPCService()
    captured_systems = []
    call_count = [0]

    class _FakeCompletions:
        def create(self, **kwargs):
            call_count[0] += 1
            captured_systems.append(kwargs["messages"][0]["content"])
            if call_count[0] == 1:
                return _make_fake_response(answer="initial answer", input_tokens=5, output_tokens=3)
            return _make_fake_response(answer="refined answer", input_tokens=7, output_tokens=4)

    _patch_openai(monkeypatch, _FakeCompletions)

    # Context that exceeds single segment â†?multi-round
    long_text = "x" * 20000  # exceeds (4096-2048-200)*4 = 7392
    result = svc.generate("q", "s", [{"content": long_text}], [])

    # Multiple calls made
    assert len(captured_systems) > 1
    # First call: context template
    assert "Use the context information below" in captured_systems[0]
    # Second call: refine template with existing_answer
    assert "refine the following existing answer" in captured_systems[1]
    assert "initial answer" in captured_systems[1]
    # Final answer is from the last refine round
    assert result["answer"] == "refined answer"


def test_generate_multi_round_accumulates_tokens(monkeypatch):
    """Token usage is accumulated across all refine rounds."""
    from app.core.config import settings
    monkeypatch.setattr(settings, "vllm_context_window", 4096)
    svc = GenerateRPCService()
    call_count = [0]

    class _FakeCompletions:
        def create(self, **kwargs):
            call_count[0] += 1
            if call_count[0] == 1:
                return _make_fake_response(answer="a1", input_tokens=10, output_tokens=5)
            return _make_fake_response(answer="a2", input_tokens=20, output_tokens=8)

    _patch_openai(monkeypatch, _FakeCompletions)

    long_text = "x" * 20000
    result = svc.generate("q", "s", [{"content": long_text}], [])

    # Multiple rounds â†?tokens accumulated
    assert call_count[0] > 1
    expected_input = 10 + 20 * (call_count[0] - 1)
    expected_output = 5 + 8 * (call_count[0] - 1)
    assert result["input_tokens"] == expected_input
    assert result["output_tokens"] == expected_output


def test_generate_multi_round_no_context_single_call(monkeypatch):
    """No context â†?single call with empty context (no refine)."""
    svc = GenerateRPCService()
    call_count = [0]

    class _FakeCompletions:
        def create(self, **kwargs):
            call_count[0] += 1
            return _make_fake_response()

    _patch_openai(monkeypatch, _FakeCompletions)

    result = svc.generate("q", "s", [], [])
    assert call_count[0] == 1
    assert result["answer"] == "test answer"


def test_generate_multi_round_empty_content_no_call(monkeypatch):
    """Context with all empty content â†?single call with empty context."""
    svc = GenerateRPCService()
    call_count = [0]

    class _FakeCompletions:
        def create(self, **kwargs):
            call_count[0] += 1
            return _make_fake_response()

    _patch_openai(monkeypatch, _FakeCompletions)

    svc.generate("q", "s", [{"content": ""}, {"content": ""}], [])
    # _repack_context returns [] for empty content â†?single call with ""
    assert call_count[0] == 1


# â”€â”€ GenerateStream â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


def test_generate_stream_yields_tokens_then_done(monkeypatch):
    """GenerateStream event sequence: token* â†?done."""
    svc = GenerateRPCService()

    class _FakeDelta:
        def __init__(self, content):
            self.content = content

    class _FakeChoice:
        def __init__(self, content):
            self.delta = _FakeDelta(content)

    class _FakeChunk:
        def __init__(self, choices=None, usage=None):
            self.choices = choices
            self.usage = usage

    chunks = [
        _FakeChunk(choices=[_FakeChoice("Hello")]),
        _FakeChunk(choices=[_FakeChoice(" world")]),
        _FakeChunk(usage=MagicMock(prompt_tokens=5, completion_tokens=2)),
    ]

    class _FakeCompletions:
        def create(self, **kwargs):
            assert kwargs.get("stream") is True
            assert kwargs.get("stream_options") == {"include_usage": True}
            return iter(chunks)

    _patch_openai(monkeypatch, _FakeCompletions)

    tokens = list(svc.generate_stream("q", "s", [], []))
    # 2 token events + 1 done event
    assert len(tokens) == 3
    assert tokens[0]["content"] == "Hello"
    assert tokens[0]["done"] is False
    assert tokens[1]["content"] == " world"
    assert tokens[1]["done"] is False
    assert tokens[2]["done"] is True
    assert tokens[2]["input_tokens"] == 5
    assert tokens[2]["output_tokens"] == 2


def test_generate_stream_timeout(monkeypatch):
    """GenerateStream timeout â†?TimeoutError."""
    svc = GenerateRPCService()

    class _FakeCompletions:
        def create(self, **kwargs):
            raise TimeoutError("stream timed out")

    _patch_openai(monkeypatch, _FakeCompletions)
    with pytest.raises(TimeoutError):
        list(svc.generate_stream("q", "s", [], []))


def test_generate_stream_uses_context_template(monkeypatch):
    """GenerateStream uses context template (not refine) â€?single call."""
    svc = GenerateRPCService()
    captured = {}

    class _FakeDelta:
        def __init__(self, content):
            self.content = content

    class _FakeChoice:
        def __init__(self, content):
            self.delta = _FakeDelta(content)

    class _FakeChunk:
        def __init__(self, choices=None, usage=None):
            self.choices = choices
            self.usage = usage

    chunks = [_FakeChunk(usage=MagicMock(prompt_tokens=1, completion_tokens=1))]

    class _FakeCompletions:
        def create(self, **kwargs):
            captured["messages"] = kwargs["messages"]
            return iter(chunks)

    _patch_openai(monkeypatch, _FakeCompletions)

    list(svc.generate_stream("q", "s", [{"content": "ctx"}], []))
    # SYSTEM message uses context template
    assert "Use the context information below" in captured["messages"][0]["content"]


# â”€â”€ Generate RPC via gRPC servicer â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€â”€


@pytest.mark.asyncio
async def test_grpc_generate_success(monkeypatch):
    """gRPC Generate RPC: question + context + history â†?answer + tokens."""
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return _make_fake_response(answer="42", input_tokens=5, output_tokens=3)

    _patch_openai(monkeypatch, _FakeCompletions)

    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.GenerateRequest(
        question="what is the answer?",
        session_id="sess1",
        context=[rag_pb.SourceChunk(content="context info", chunk_id="c1")],
        history=[
            rag_pb.ChatMessage(role="user", content="what is the answer?"),
        ],
        max_tokens=1024,
    )
    resp = await servicer.Generate(req, ctx)
    assert ctx.aborted_code is None
    assert resp.answer == "42"
    assert resp.input_tokens == 5
    assert resp.output_tokens == 3
    assert resp.session_id == "sess1"
    # Single segment â†?single call
    assert len(captured) == 1
    # Messages: SYSTEM + 1 history + 1 question = 3
    assert len(captured[0]["messages"]) == 3


@pytest.mark.asyncio
async def test_grpc_generate_timeout_deadline_exceeded(monkeypatch):
    """gRPC Generate RPC: timeout â†?DEADLINE_EXCEEDED."""
    class _FakeCompletions:
        def create(self, **kwargs):
            raise TimeoutError("timed out")

    _patch_openai(monkeypatch, _FakeCompletions)

    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.GenerateRequest(question="q", session_id="s")
    with pytest.raises(Exception):  # noqa: B017
        await servicer.Generate(req, ctx)
    assert ctx.aborted_code == grpc.StatusCode.DEADLINE_EXCEEDED


@pytest.mark.asyncio
async def test_grpc_generate_default_max_tokens(monkeypatch):
    """max_tokens=0 â†?default 2048."""
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return _make_fake_response()

    _patch_openai(monkeypatch, _FakeCompletions)

    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.GenerateRequest(question="q", session_id="s", max_tokens=0)
    await servicer.Generate(req, ctx)
    assert captured[0]["max_tokens"] == 2048


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
