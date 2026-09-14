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
    ASCII_CHARS_PER_TOKEN,
    CONTEXT_OVERHEAD_TOKENS,
    DEFAULT_CONTEXT_TEMPLATE,
    DEFAULT_REFINE_TEMPLATE,
    MESSAGE_OVERHEAD_TOKENS,
    MIN_CONTEXT_TOKENS,
    TOKEN_SAFETY_MARGIN,
    GenerateRPCService,
    _estimate_messages_tokens,
    _estimate_tokens,
    _max_completion_tokens,
    _repack_context,
    _truncate_history,
    _truncate_to_tokens,
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
    """Context longer than the token budget is truncated (CJK-aware)."""
    from app.core.config import settings

    monkeypatch.setattr(settings, "vllm_context_window", 1024)
    svc = GenerateRPCService()
    # completion = min(2048, 1024-512) = 512
    # available = 1024 - 512 - 80 - 32 - q_tokens - 6
    long_text = "x" * 10000
    context = [{"content": long_text}]
    result = svc._build_context_str(context, question="q")
    budget = (
        1024 - 512 - CONTEXT_OVERHEAD_TOKENS - TOKEN_SAFETY_MARGIN - 1 - 6
    )
    # ASCII: 4 chars/token → cut at budget*4 chars, estimate stays ≤ budget
    assert len(result) == budget * ASCII_CHARS_PER_TOKEN
    assert _estimate_tokens(result) <= budget
    assert result == long_text[: budget * ASCII_CHARS_PER_TOKEN]


def test_build_context_str_truncates_chinese_context(monkeypatch):
    """中文 context 不再被 4-chars/token 低估（回归：vLLM 400 场景）。

    1848 个中文字旧估算 = 462 tokens（放行），Qwen 实际 ~1800 tokens
    → 溢出 1024 窗口。CJK 感知估算后单字 1 token，被截到预算内。
    """
    from app.core.config import settings

    monkeypatch.setattr(settings, "vllm_context_window", 1024)
    svc = GenerateRPCService()
    chinese = "测" * 1848  # 旧估算: 1848//4 = 462 tokens (under-counted)
    context = [{"content": chinese}]
    result = svc._build_context_str(context, question="你好")
    budget = (
        1024
        - 512
        - CONTEXT_OVERHEAD_TOKENS
        - TOKEN_SAFETY_MARGIN
        - 2  # "你好" = 2 CJK tokens
        - 6
    )
    # 新估算: 1848 CJK = 1848 tokens > budget → 截断
    assert len(result) <= budget
    assert _estimate_tokens(result) <= budget
    assert result == "测" * budget


def test_max_context_tokens_small_window(monkeypatch):
    """A 1024-token dev model still gets a usable (>= MIN) context budget."""
    from app.core.config import settings

    monkeypatch.setattr(settings, "vllm_context_window", 1024)
    svc = GenerateRPCService()
    # completion = max(64, min(2048, 1024-512)) = 512
    # available = 1024 - 512 - 80 - 32 - 2(你好) - 6 = 392
    # history_cap = 392 // 4 = 98 → empty history costs 0
    # ctx_budget = 392 - 0 = 392, floored at MIN_CONTEXT_TOKENS
    budget, trimmed_history = svc._max_context_tokens("你好", [])
    assert trimmed_history == []
    assert budget >= MIN_CONTEXT_TOKENS
    assert budget == (
        1024
        - 512
        - CONTEXT_OVERHEAD_TOKENS
        - TOKEN_SAFETY_MARGIN
        - 2
        - MESSAGE_OVERHEAD_TOKENS
    )


def test_max_context_tokens_history_capped(monkeypatch):
    """History is capped at 1/4 of the remaining budget (oldest dropped)."""
    from app.core.config import settings

    monkeypatch.setattr(settings, "vllm_context_window", 1024)
    svc = GenerateRPCService()
    # available = 392 (same as above) → history_cap = 98 tokens
    history = [
        {"role": "user", "content": "旧消息一"},
        {"role": "assistant", "content": "旧回答二"},
        {"role": "user", "content": "你好"},
    ]
    budget, trimmed = svc._max_context_tokens("你好", history)
    # 3 messages ≈ 3*(content + 6) tokens; 旧消息一(4)+6 + 旧回答二(4)+6
    # + 你好(2)+6 = 28 ≤ 98 → all kept, budget reduced accordingly
    assert len(trimmed) == 3
    assert budget == 392 - _estimate_messages_tokens(trimmed)
    # Long history: oldest dropped first
    long_history = [
        {"role": "user", "content": "很长的历史消息" * 50},
        {"role": "assistant", "content": "很长的回答" * 50},
    ]
    _, trimmed2 = svc._max_context_tokens("你好", long_history)
    assert len(trimmed2) < len(long_history)
    assert _estimate_messages_tokens(trimmed2) <= 98


def test_max_completion_tokens_clamped_to_window(monkeypatch):
    """Small models never request more completion tokens than the window."""
    from app.core.config import settings

    monkeypatch.setattr(settings, "vllm_context_window", 1024)
    # window 1024 → reserve 512 → completion 512 (not the default 2048)
    assert _max_completion_tokens() == 512
    monkeypatch.setattr(settings, "vllm_context_window", 32768)
    # large window → capped at 2048 as before
    assert _max_completion_tokens() == 2048


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
    """Context exceeding max split into multiple segments (token budget)."""
    long_text = "x" * 100
    context = [{"content": long_text}]
    segments = _repack_context(context, 10)
    # ASCII: 4 chars/token, budget 10 tokens = 40 chars per segment
    # 100 chars = 40 + 40 + 20 = 3 segments
    assert len(segments) == 3
    assert len(segments[0]) == 40
    assert len(segments[1]) == 40
    assert len(segments[2]) == 20
    # Verify total content preserved
    assert "".join(segments) == long_text


def test_repack_context_cjk_segments():
    """CJK context: 1 char = 1 token (not 1/4) - the vLLM 400 regression."""
    chinese = "测" * 200  # 200 CJK chars = 200 tokens
    context = [{"content": chinese}]
    segments = _repack_context(context, 50)
    # Old char-based estimate: 200 chars would be one segment (overflow).
    # CJK-aware: 200 tokens / 50 = 4 segments, 50 chars each.
    assert len(segments) == 4
    assert all(len(s) == 50 for s in segments)
    assert "".join(segments) == chinese


def test_repack_context_mixed_cjk_ascii():
    """Mixed CJK+ASCII text: per-char classification keeps the bound."""
    # 4 CJK chars (4 tokens) + 6 non-CJK chars " world" (ceil(6/4)=2)
    # → total 6 tokens; per-char classification handles the mix.
    mixed = "你好世界 world"
    assert _estimate_tokens(mixed) == 6
    # 200 CJK + 400 ASCII = 200 + 100 = 300 tokens
    text = "测" * 200 + "x" * 400
    context = [{"content": text}]
    segments = _repack_context(context, 100)
    assert all(_estimate_tokens(s) <= 100 for s in segments)
    assert "".join(segments) == text


def test_repack_context_joins_before_splitting():
    """Multiple chunks are joined with \\n\\n before splitting."""
    context = [{"content": "aaa"}, {"content": "bbb"}]
    segments = _repack_context(context, 10)
    # "aaa\n\nbbb" = 9 chars = ceil(9/4) = 3 tokens, fits in 10
    assert len(segments) == 1
    assert segments[0] == "aaa\n\nbbb"


# ── _estimate_tokens / _truncate_to_tokens / _truncate_history ────────────────


def test_estimate_tokens_cjk_and_ascii():
    """CJK chars count as 1 token each; ASCII at 4 chars per token."""
    assert _estimate_tokens("") == 0
    assert _estimate_tokens("hello") == 2  # ceil(5/4)
    assert _estimate_tokens("你好") == 2  # 1 token per CJK char
    assert _estimate_tokens("你好 world") == 4  # 2 CJK + ceil(7/4)
    assert _estimate_tokens("你好世界") == 4


def test_truncate_to_tokens_ascii_ceil_boundary():
    """Tail ASCII run counts via ceil: budget 1 fits exactly 4 chars.

    Regression: the first version only counted full 4-char runs and let
    a 5-char tail through, overflowing the budget.
    """
    assert _truncate_to_tokens("aaaa", 1) == "aaaa"
    assert _truncate_to_tokens("aaaaa", 1) == "aaaa"
    assert _truncate_to_tokens("aa", 1) == "aa"
    assert _truncate_to_tokens("", 5) == ""
    assert _truncate_to_tokens("abc", 0) == ""


def test_truncate_to_tokens_cjk():
    """CJK text truncates at exactly max_tokens chars."""
    assert _truncate_to_tokens("你好世界", 2) == "你好"
    assert _truncate_to_tokens("测" * 100, 50) == "测" * 50
    # Mixed: 2 CJK + 4 ASCII = 3 tokens, budget 2 → only the CJK part.
    assert _truncate_to_tokens("你好abcd", 2) == "你好"


def test_truncate_history_keeps_newest():
    """Oldest messages are dropped first; newest survives alone."""
    history = [
        {"role": "user", "content": "a"},  # oldest
        {"role": "assistant", "content": "b"},
        {"role": "user", "content": "c"},  # newest
    ]
    # Budget fits exactly one message (1 token + 6 overhead)
    kept = _truncate_history(history, 7)
    assert kept == [{"role": "user", "content": "c"}]
    assert _truncate_history([], 100) == []
    # Newest alone exceeding the budget → truncated copy, not dropped.
    kept2 = _truncate_history(
        [{"role": "user", "content": "测" * 100}], 20
    )
    assert len(kept2) == 1
    assert len(kept2[0]["content"]) == 14  # 20 - 6 overhead


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
    """max_tokens=0 â†' default 2048."""
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


# ── Per-request LLM model routing (inference_service_name) ──────────────────


def test_generate_routes_model_param(monkeypatch):
    """Generate passes inference_service_name as the model kwarg."""
    svc = GenerateRPCService()
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return _make_fake_response()

    _patch_openai(monkeypatch, _FakeCompletions)

    svc.generate(
        question="q",
        session_id="s",
        context=[{"content": "ctx"}],
        history=[],
        inference_service_name="qwen3-8b",
    )
    assert captured[0]["model"] == "qwen3-8b"


def test_generate_default_model_fallback(monkeypatch):
    """Empty inference_service_name falls back to settings.vllm_model."""
    from app.core.config import settings

    svc = GenerateRPCService()
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return _make_fake_response()

    _patch_openai(monkeypatch, _FakeCompletions)

    svc.generate(
        question="q",
        session_id="s",
        context=[{"content": "ctx"}],
        history=[],
        inference_service_name="",
    )
    assert captured[0]["model"] == settings.vllm_model


def test_generate_multi_round_model_param_all_rounds(monkeypatch):
    """Multi-round generate routes the model on every round."""
    from app.core.config import settings

    monkeypatch.setattr(settings, "vllm_context_window", 4096)
    svc = GenerateRPCService()
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return _make_fake_response(answer="a")

    _patch_openai(monkeypatch, _FakeCompletions)

    long_text = "x" * 20000  # forces multi-round
    svc.generate("q", "s", [{"content": long_text}], [], inference_service_name="svc-a")
    assert len(captured) > 1
    for call in captured:
        assert call["model"] == "svc-a"


def test_generate_stream_routes_model_param(monkeypatch):
    """GenerateStream passes inference_service_name as the model kwarg."""
    svc = GenerateRPCService()

    class _FakeChunk:
        def __init__(self, choices=None, usage=None):
            self.choices = choices
            self.usage = usage

    chunks = [_FakeChunk(usage=MagicMock(prompt_tokens=1, completion_tokens=1))]
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return iter(chunks)

    _patch_openai(monkeypatch, _FakeCompletions)

    list(
        svc.generate_stream(
            "q", "s", [{"content": "ctx"}], [], inference_service_name="qwen3-8b"
        )
    )
    assert captured[0]["model"] == "qwen3-8b"


def test_generate_stream_default_model_fallback(monkeypatch):
    """GenerateStream empty inference_service_name falls back to settings."""
    from app.core.config import settings

    svc = GenerateRPCService()

    class _FakeChunk:
        def __init__(self, choices=None, usage=None):
            self.choices = choices
            self.usage = usage

    chunks = [_FakeChunk(usage=MagicMock(prompt_tokens=1, completion_tokens=1))]
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return iter(chunks)

    _patch_openai(monkeypatch, _FakeCompletions)

    list(svc.generate_stream("q", "s", [], [], inference_service_name=""))
    assert captured[0]["model"] == settings.vllm_model


@pytest.mark.asyncio
async def test_grpc_generate_inference_service_name_routed(monkeypatch):
    """gRPC Generate RPC: inference_service_name → model kwarg."""
    captured = []

    class _FakeCompletions:
        def create(self, **kwargs):
            captured.append(kwargs)
            return _make_fake_response()

    _patch_openai(monkeypatch, _FakeCompletions)

    servicer = RagEngineServicer()
    ctx = FakeContext()
    req = rag_pb.GenerateRequest(
        question="q",
        session_id="s",
        inference_service_name="qwen3-8b",
    )
    await servicer.Generate(req, ctx)
    assert ctx.aborted_code is None
    assert captured[0]["model"] == "qwen3-8b"


if __name__ == "__main__":
    raise SystemExit(pytest.main([__file__, "-v"]))
