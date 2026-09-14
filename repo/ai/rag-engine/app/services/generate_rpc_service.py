"""Generate RPC service (RAG-REFACTOR-STEP-2 / Plan §2.4).

Stateless LLM generation using the pure Python ``openai`` SDK (no LlamaIndex
dependency). Reproduces the LlamaIndex ``ContextChatEngine`` +
``CompactAndRefine`` synthesizer behavior:

1. **Context repack** (reproduces ``CompactAndRefine._make_compact_text_chunks``):
   joins retrieved chunk texts and splits them into segments that each fit
   within the LLM context window (CJK-aware token estimate: a CJK char ≈ 1
   token, ASCII ≈ 4 chars per token). Each segment becomes a separate LLM
   call round.
2. **First round** (reproduces ``get_response_synthesizer`` initial call):
   ``[SYSTEM: DEFAULT_CONTEXT_TEMPLATE.format(context_str=segment_1),
   *chat_history, USER: question]``.
   ``chat_history`` includes the current-turn user message (reproduces the
   legacy behavior where kb-service appends user to Redis before calling
   rag-engine). ``question`` is appended as the final USER message
   (reproduces the ``{query_str}`` template — the current-turn user appears
   twice).
3. **Refine rounds** (reproduces ``CompactAndRefine._run_refine_loop``):
   for each subsequent context segment, the LLM is called with
   ``[SYSTEM: DEFAULT_REFINE_TEMPLATE.format(context_msg=segment_i,
   existing_answer=prev_answer), *chat_history, USER: question]``.
   The LLM refines the existing answer using the new context segment.
4. **Token accumulation**: ``input_tokens`` and ``output_tokens`` are summed
   across all LLM calls (matches LlamaIndex's ``CompactAndRefine`` which
   accumulates usage across refine rounds).
5. **LLM call**: ``openai.OpenAI.chat.completions.create`` with 120s timeout
   (matches the old ``OpenAILike(timeout=120.0)``).

When the context fits in a single segment (the common case), only one LLM
call is made — equivalent to the old single-chunk path.
"""
from __future__ import annotations

import functools
import logging
import time
from collections.abc import Iterator
from typing import Any

from app.core.config import settings

logger = logging.getLogger(__name__)

# Reproduce LlamaIndex ContextChatEngine default prompts (Plan §2.4).
DEFAULT_CONTEXT_TEMPLATE = (
    "Use the context information below to assist the user."
    "\n--------------------\n"
    "{context_str}"
    "\n--------------------\n"
)

DEFAULT_REFINE_TEMPLATE = (
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

# vLLM request timeout (matches old OpenAILike timeout=120.0).
LLM_TIMEOUT_SECONDS = 120.0
# Wall-clock budget for the whole multi-round generate (all segments).
# The kb-service gRPC client applies a fixed 120s deadline to the Generate
# RPC (RagEngineGRPCClient._timeout); without a server-side total budget,
# N refine rounds × 120s each could exceed it and the whole answer would be
# discarded as DEADLINE_EXCEEDED. The budget is slightly under the client
# deadline so a round that would blow the deadline is skipped early and the
# already-computed answer is returned instead.
GENERATE_TOTAL_BUDGET_SECONDS = 110.0
# ASCII chars per token (English density; Plan §2.4). CJK chars are counted
# individually — a CJK char is ~1 token for Qwen-family tokenizers, so the
# old flat 4-chars-per-token estimate under-counted Chinese text by ~4x and
# overflowed small (1024-token) dev windows with a vLLM 400.
ASCII_CHARS_PER_TOKEN = 4
# Tokens reserved for prompt template literals + chat formatting beyond
# question/history/context. Covers the longest template (refine, ~60 tokens)
# with headroom.
CONTEXT_OVERHEAD_TOKENS = 80
# Minimum context tokens for a segment so tiny models still get usable context.
MIN_CONTEXT_TOKENS = 50
# Extra tokens shaved off every budget so estimate error can never overflow
# the window (the estimate only stays an upper bound with a safety margin).
TOKEN_SAFETY_MARGIN = 32
# Per-message overhead tokens (chat template tags: <|im_start|>role\\n .. <|im_end|>).
MESSAGE_OVERHEAD_TOKENS = 6


def _is_cjk(ch: str) -> bool:
    """True if ``ch`` is a CJK/fullwidth char (≈1 token each in Qwen BPE)."""
    code = ord(ch)
    return (
        0x2E80 <= code <= 0x9FFF  # CJK radicals..CJK Unified Ideographs
        or 0xAC00 <= code <= 0xD7AF  # Hangul syllables
        or 0xF900 <= code <= 0xFAFF  # CJK compatibility ideographs
        or 0xFF00 <= code <= 0xFF60  # fullwidth forms
        or 0x3000 <= code <= 0x303F  # CJK punctuation
    )


def _estimate_tokens(text: str) -> int:
    """Estimate the token count of ``text`` (conservative upper bound).

    Per-char classification: CJK chars count as 1 token each (Qwen BPE
    is mostly one-token-per-char for Han/Hangul), other chars at
    ``ASCII_CHARS_PER_TOKEN`` per token. Mixed CJK+ASCII text is counted
    per category, so the bound holds for any mix.
    """
    if not text:
        return 0
    cjk = 0
    other = 0
    for ch in text:
        if _is_cjk(ch):
            cjk += 1
        else:
            other += 1
    return cjk + (other + ASCII_CHARS_PER_TOKEN - 1) // ASCII_CHARS_PER_TOKEN


def _estimate_messages_tokens(messages: list[dict]) -> int:
    """Estimate the prompt token count of a chat message list."""
    return sum(
        _estimate_tokens(m.get("content", "")) + MESSAGE_OVERHEAD_TOKENS
        for m in messages
    )


def _truncate_to_tokens(text: str, max_tokens: int) -> str:
    """Truncate ``text`` so its estimated tokens stay within ``max_tokens``.

    Cuts at the char boundary where the running estimate reaches the budget
    (head truncation — keep the beginning, matching the old [:max] cut).
    """
    if max_tokens <= 0 or not text:
        return ""
    used = 0
    other_run = 0  # pending non-CJK chars not yet promoted to a token
    for i, ch in enumerate(text):
        if _is_cjk(ch):
            used += 1 + (other_run + ASCII_CHARS_PER_TOKEN - 1) // ASCII_CHARS_PER_TOKEN
            other_run = 0
        else:
            other_run += 1
        pending = (
            (other_run + ASCII_CHARS_PER_TOKEN - 1) // ASCII_CHARS_PER_TOKEN
            if other_run
            else 0
        )
        if used + pending > max_tokens:
            return text[:i]
    return text


def _truncate_history(history: list[dict], max_tokens: int) -> list[dict]:
    """Keep the most recent history messages within ``max_tokens``.

    Oldest messages are dropped first (chat convention). If the newest
    message alone exceeds the budget, its content is truncated.
    """
    if not history or max_tokens <= MESSAGE_OVERHEAD_TOKENS:
        return []
    kept: list[dict] = []
    used = 0
    for msg in reversed(history):
        content = msg.get("content", "")
        cost = _estimate_tokens(content) + MESSAGE_OVERHEAD_TOKENS
        if used + cost <= max_tokens:
            kept.append(msg)
            used += cost
            continue
        if not kept:
            # Newest message alone exceeds the budget → truncated copy.
            kept.append(
                {
                    "role": msg.get("role", "user"),
                    "content": _truncate_to_tokens(
                        content, max_tokens - MESSAGE_OVERHEAD_TOKENS
                    ),
                }
            )
        break  # older messages no longer fit
    kept.reverse()
    return kept


def _max_completion_tokens() -> int:
    """Max completion tokens per LLM call, clamped to the model's window.

    ``messages + max_tokens`` must fit ``vllm_context_window``; small dev
    models (1024-token) reject the default 2048 with a 400. Reserves 512
    tokens for messages, floor 64.
    """
    budget = settings.vllm_context_window - 512
    return max(64, min(2048, budget))


def _answer_reserve_tokens(max_tokens: int) -> int:
    """Tokens reserved for the previous round's answer in refine prompts.

    Refine rounds carry the previous answer inside the prompt; reserving a
    slice of the completion budget (and truncating longer answers to it)
    keeps ``messages + completion`` inside the window even on tiny dev
    models. Production (32k) windows barely notice the reserve.
    """
    return max(64, max_tokens // 4)


@functools.lru_cache(maxsize=1)
def _import_openai_exceptions():
    """Import openai SDK exception classes (cached at module level).

    The openai SDK error hierarchy (v1.x)::

        OpenAIError
        ├── APIError
        │   ├── APIConnectionError
        │   │   └── APITimeoutError
        │   ├── APIResponseValidationError
        │   └── APIStatusError
        │       ├── BadRequestError (400)
        │       ├── AuthenticationError (401)
        │       ├── ...
        │       └── InternalServerError (5xx)

    Returns a dict of exception classes. If the import fails or the openai
    module is a stub (MagicMock), returns a fallback dict of dummy classes
    so ``isinstance`` checks never match.
    """
    class _Dummy(Exception):
        pass

    fallback = {
        "APIError": _Dummy,
        "APIConnectionError": _Dummy,
        "APITimeoutError": _Dummy,
        "APIStatusError": _Dummy,
    }
    try:
        import openai

        result = {}
        for key, attr_name in (
            ("APIError", "APIError"),
            ("APIConnectionError", "APIConnectionError"),
            ("APITimeoutError", "APITimeoutError"),
            ("APIStatusError", "APIStatusError"),
        ):
            val = getattr(openai, attr_name, None)
            # Verify it's a real type (not a MagicMock stub attribute).
            if isinstance(val, type) and issubclass(val, BaseException):
                result[key] = val
            else:
                result[key] = fallback[key]
        return result
    except Exception:  # noqa: BLE001 — openai may be stubbed in tests
        return fallback


def _map_openai_exception(exc: Exception) -> Exception:
    """Map an openai SDK exception to a plain exception for the gRPC layer.

    - ``APITimeoutError`` → ``TimeoutError`` (→ gRPC DEADLINE_EXCEEDED)
    - ``APIConnectionError`` → ``RuntimeError`` (→ gRPC UNAVAILABLE)
    - ``APIStatusError`` (5xx) → ``RuntimeError`` (→ gRPC INTERNAL)
    - ``APIError`` (generic) → ``RuntimeError`` (→ gRPC INTERNAL)

    Also handles built-in Python exceptions that the openai SDK subclasses:
    - ``TimeoutError`` (base of ``APITimeoutError``) → re-raise as-is
    - ``ConnectionError`` (base of ``APIConnectionError``) → ``RuntimeError``

    Falls back to ``RuntimeError`` for unknown exceptions.
    """
    excs = _import_openai_exceptions()
    # Check APITimeoutError first (it's a subclass of APIConnectionError).
    if isinstance(exc, excs["APITimeoutError"]):
        raise TimeoutError(str(exc)) from exc
    if isinstance(exc, excs["APIConnectionError"]):
        raise RuntimeError(f"vLLM unavailable: {exc}") from exc  # noqa: TRY004
    if isinstance(exc, excs["APIError"]):
        raise RuntimeError(f"vLLM error: {exc}") from exc  # noqa: TRY004
    # Built-in Python exceptions (openai SDK subclasses these but tests
    # may raise the base types directly).
    if isinstance(exc, TimeoutError):
        raise exc
    if isinstance(exc, ConnectionError):
        raise RuntimeError(f"vLLM unavailable: {exc}") from exc  # noqa: TRY004
    raise RuntimeError(f"vLLM error: {exc}") from exc


def _repack_context(context: list[dict], max_context_tokens: int) -> list[str]:
    """Split context texts into segments each fitting ``max_context_tokens``.

    Reproduces LlamaIndex ``CompactAndRefine._make_compact_text_chunks`` /
    ``PromptHelper.repack`` behavior (token-budget aware, CJK-safe): each
    retrieved chunk's text is joined with ``\\n\\n`` and the combined text
    is split into segments of at most ``max_context_tokens`` estimated
    tokens (a CJK char costs 1 token, not 1/4).

    A single chunk that exceeds the limit is split at the boundary (the
    remainder is truncated to the max, matching the old rough-truncation
    behavior for oversized single chunks).

    Returns:
        List of context segment strings. Each segment will be used in a
        separate LLM call round (first = QA, subsequent = refine).
    """
    if not context:
        return []
    # Join all chunk texts with double newline (matches old _build_context_str).
    full_text = "\n\n".join(c.get("content", "") for c in context)
    if not full_text.strip():
        return []
    if _estimate_tokens(full_text) <= max_context_tokens:
        return [full_text]
    # Split into segments of max_context_tokens (CJK-aware token estimate).
    segments: list[str] = []
    start = 0
    while start < len(full_text):
        segment = _truncate_to_tokens(full_text[start:], max_context_tokens)
        if not segment:
            break
        segments.append(segment)
        start += len(segment)
    return segments


class GenerateRPCService:
    """Stateless LLM generation service (Plan §2.4).

    Uses the pure Python ``openai`` SDK to call vLLM ``/v1/chat/completions``.
    Does NOT depend on LlamaIndex. Multi-turn history is passed in by the
    caller (includes the current-turn user message, reproducing the legacy
    behavior).
    """

    DEFAULT_CONTEXT_TEMPLATE = DEFAULT_CONTEXT_TEMPLATE
    DEFAULT_REFINE_TEMPLATE = DEFAULT_REFINE_TEMPLATE

    def __init__(self) -> None:
        # Reuse a single OpenAI client per service instance to avoid
        # leaking httpx connection pools on every request.
        self._client: Any = None

    def _make_client(self) -> Any:
        """Return a cached ``openai.OpenAI`` client (created lazily).

        The client carries an httpx connection pool; reusing it avoids
        pool leaks. Tests can monkeypatch this method to inject a fake.
        """
        if self._client is not None:
            return self._client
        import openai

        self._client = openai.OpenAI(
            base_url=settings.vllm_api_base,
            api_key=settings.vllm_api_key or "EMPTY",
            timeout=LLM_TIMEOUT_SECONDS,
        )
        return self._client

    def close(self) -> None:
        """Close the cached OpenAI client (release httpx connection pool)."""
        if self._client is not None:
            try:
                self._client.close()
            except Exception:  # noqa: BLE001, S110 — best-effort close
                pass
            self._client = None

    def _max_context_tokens(
        self,
        question: str,
        history: list[dict],
        existing_answer: str = "",
    ) -> tuple[int, list[dict]]:
        """Compute the per-round context token budget + truncated history.

        Budget = window − completion_reserve − overhead − safety −
        question − existing_answer (refine rounds) − history (capped at
        1/4 of the remainder). History is truncated to its cap and
        returned alongside the budget so every round reuses the same
        trimmed list — this is what keeps the *whole* message list under
        the window (the old char-based budget only counted context).

        Small dev models (1024-token) get a proportionally small budget;
        production (32k) windows keep the full context in one segment.
        """
        available = (
            settings.vllm_context_window
            - _max_completion_tokens()
            - CONTEXT_OVERHEAD_TOKENS
            - TOKEN_SAFETY_MARGIN
            - _estimate_tokens(question)
            - MESSAGE_OVERHEAD_TOKENS
            - _estimate_tokens(existing_answer)
        )
        history_cap = max(0, available // 4)
        trimmed_history = _truncate_history(history, history_cap)
        ctx_budget = available - _estimate_messages_tokens(trimmed_history)
        return max(MIN_CONTEXT_TOKENS, ctx_budget), trimmed_history

    def _build_context_str(
        self,
        context: list[dict],
        question: str = "",
        history: list[dict] | None = None,
        budget: int | None = None,
    ) -> str:
        """Assemble + truncate context to the token budget (single segment).

        Kept for backward compatibility with tests. For the full
        multi-round CompactAndRefine behavior, ``_repack_context`` +
        ``generate`` should be used instead.
        """
        if not context:
            return ""
        context_str = "\n\n".join(c.get("content", "") for c in context)
        if budget is None:
            budget, _ = self._max_context_tokens(question, history or [])
        return _truncate_to_tokens(context_str, budget)

    def _build_initial_messages(
        self,
        question: str,
        context_str: str,
        history: list[dict],
    ) -> list[dict]:
        """Build messages for the first (QA) round.

        Sequence: ``[SYSTEM: context_template, *chat_history, USER: question]``.
        """
        system_prompt = self.DEFAULT_CONTEXT_TEMPLATE.format(context_str=context_str)
        messages: list[dict] = [{"role": "system", "content": system_prompt}]
        for msg in history:
            messages.append({"role": msg.get("role", "user"), "content": msg.get("content", "")})
        messages.append({"role": "user", "content": question})
        return messages

    def _build_refine_messages(
        self,
        question: str,
        context_msg: str,
        existing_answer: str,
        history: list[dict],
    ) -> list[dict]:
        """Build messages for a refine round.

        Sequence: ``[SYSTEM: refine_template, *chat_history, USER: question]``.
        The refine template includes ``context_msg`` (the current segment) and
        ``existing_answer`` (the answer from the previous round).
        """
        system_prompt = self.DEFAULT_REFINE_TEMPLATE.format(
            context_msg=context_msg,
            existing_answer=existing_answer,
        )
        messages: list[dict] = [{"role": "system", "content": system_prompt}]
        for msg in history:
            messages.append({"role": msg.get("role", "user"), "content": msg.get("content", "")})
        messages.append({"role": "user", "content": question})
        return messages

    def _build_messages(
        self,
        question: str,
        context: list[dict],
        history: list[dict],
    ) -> list[dict]:
        """Build the chat messages for the first round (backward compat).

        This is a convenience wrapper that builds initial messages using a
        single (truncated) context segment. For the full multi-round
        CompactAndRefine behavior, ``generate`` uses ``_repack_context`` +
        ``_build_initial_messages`` + ``_build_refine_messages`` internally.
        """
        context_str = self._build_context_str(context)
        return self._build_initial_messages(question, context_str, history)

    def _call_llm(
        self,
        client: Any,
        messages: list[dict],
        max_tokens: int,
        model: str = "",
    ) -> tuple[str, int, int]:
        """Make a single LLM call and return (answer, input_tokens, output_tokens).

        Args:
            client: OpenAI-compatible client (base_url unchanged per model).
            messages: Chat messages.
            max_tokens: Max output tokens.
            model: Per-request model name (served_model_name routed by the
                AI Gateway); empty falls back to ``settings.vllm_model``.

        Raises:
            TimeoutError: vLLM timed out.
            RuntimeError: vLLM unavailable / API error.
        """
        try:
            response = client.chat.completions.create(
                model=model or settings.vllm_model,
                messages=messages,
                max_tokens=max_tokens,
            )
        except TimeoutError:
            raise
        except Exception as exc:  # noqa: BLE001
            _map_openai_exception(exc)

        answer = ""
        if response.choices:
            answer = response.choices[0].message.content or ""
        input_tokens = 0
        output_tokens = 0
        if response.usage:
            input_tokens = response.usage.prompt_tokens or 0
            output_tokens = response.usage.completion_tokens or 0
        return answer, input_tokens, output_tokens

    def generate(
        self,
        question: str,
        session_id: str,
        context: list[dict],
        history: list[dict],
        inference_service_name: str = "",
        max_tokens: int = 2048,
    ) -> dict:
        """Run LLM completion with CompactAndRefine multi-round synthesis.

        Reproduces LlamaIndex ``ContextChatEngine`` + ``CompactAndRefine``:

        1. Context texts are repacked into segments fitting the LLM context
           window.
        2. First segment: QA call with ``DEFAULT_CONTEXT_TEMPLATE``.
        3. Subsequent segments: refine calls with ``DEFAULT_REFINE_TEMPLATE``
           (includes ``existing_answer`` from the previous round).
        4. Token usage is accumulated across all rounds.

        Args:
            question: User question; appended as USER at history end.
            session_id: Session ID (echoed back in the response).
            context: Retrieved source chunks (list of dicts with ``content``).
            history: Chat history (includes current-turn user message).
            inference_service_name: Per-request model name (served_model_name
                routed by the AI Gateway); empty falls back to the default
                ``settings.vllm_model``.
            max_tokens: Max output tokens per round.

        Returns:
            ``{"answer", "input_tokens", "output_tokens", "session_id"}``.

        Raises:
            TimeoutError: vLLM timed out (→ gRPC DEADLINE_EXCEEDED).
            RuntimeError: vLLM unavailable / API error.
        """
        client = self._make_client()
        max_tokens = min(max_tokens, _max_completion_tokens())

        # Dynamic per-round budget: window − completion − overhead −
        # question − history (capped) − safety. Refine rounds re-budget
        # with the previous answer's cost so prompts never overflow.
        first_budget, trimmed_history = self._max_context_tokens(
            question, history
        )
        segments = _repack_context(context, first_budget)

        # No context → single call with empty context (matches old behavior).
        if not segments:
            messages = self._build_initial_messages(
                question, "", trimmed_history
            )
            answer, input_tokens, output_tokens = self._call_llm(
                client, messages, max_tokens, model=inference_service_name
            )
            return {
                "answer": answer,
                "input_tokens": input_tokens,
                "output_tokens": output_tokens,
                "session_id": session_id,
            }

        # First round: QA call with the first context segment.
        answer, input_tokens, output_tokens = self._call_llm(
            client,
            self._build_initial_messages(
                question, segments[0], trimmed_history
            ),
            max_tokens,
            model=inference_service_name,
        )

        # Refine rounds: for each subsequent segment, refine the answer.
        # Total wall-clock budget: stop refining (keep the current answer)
        # when the next round would risk blowing the kb-service client's
        # fixed 120s Generate deadline — a partial answer beats losing the
        # whole response to DEADLINE_EXCEEDED.
        deadline = time.monotonic() + GENERATE_TOTAL_BUDGET_SECONDS
        answer_cap = _answer_reserve_tokens(max_tokens)
        for round_no, segment in enumerate(segments[1:], start=2):
            if time.monotonic() >= deadline:
                logger.warning(
                    "generate: total budget %.0fs exhausted before refine "
                    "round %d/%d; returning current answer",
                    GENERATE_TOTAL_BUDGET_SECONDS,
                    round_no,
                    len(segments),
                )
                break
            if _estimate_tokens(answer) > answer_cap:
                answer = _truncate_to_tokens(answer, answer_cap)
            refine_budget, refine_history = self._max_context_tokens(
                question, history, answer
            )
            segment = _truncate_to_tokens(segment, refine_budget)
            answer, in_tok, out_tok = self._call_llm(
                client,
                self._build_refine_messages(
                    question, segment, answer, refine_history
                ),
                max_tokens,
                model=inference_service_name,
            )
            input_tokens += in_tok
            output_tokens += out_tok

        return {
            "answer": answer,
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "session_id": session_id,
        }

    def generate_stream(
        self,
        question: str,
        session_id: str,
        context: list[dict],
        history: list[dict],
        inference_service_name: str = "",
        max_tokens: int = 2048,
    ) -> Iterator[dict]:
        """Stream LLM tokens (Plan §2.4 GenerateStream).

        Streaming does NOT use multi-round refine — it makes a single LLM
        call with the full (truncated) context. This matches the old
        streaming path behavior where ``ContextChatEngine.stream_chat``
        uses a single ``CompactAndRefine`` call (streaming refine is not
        supported by LlamaIndex either).

        Yields dict events:
          ``{"content": str, "done": False}`` for each token chunk.
          ``{"content": "", "done": True, "input_tokens": int, "output_tokens": int}``
          as the final event (usage from the last chunk via
          ``stream_options={"include_usage": True}``).
        """
        # Single-round budget (same formula as generate's first round);
        # context is truncated to it and history to its cap so the whole
        # message list fits the window.
        budget, trimmed_history = self._max_context_tokens(question, history)
        context_str = self._build_context_str(
            context, question, history, budget
        )
        messages = self._build_initial_messages(
            question, context_str, trimmed_history
        )
        client = self._make_client()
        max_tokens = min(max_tokens, _max_completion_tokens())
        try:
            stream = client.chat.completions.create(
                model=inference_service_name or settings.vllm_model,
                messages=messages,
                max_tokens=max_tokens,
                stream=True,
                stream_options={"include_usage": True},
            )
        except TimeoutError:
            raise
        except Exception as exc:  # noqa: BLE001
            _map_openai_exception(exc)

        for chunk in stream:
            if chunk.choices and chunk.choices[0].delta.content:
                yield {"content": chunk.choices[0].delta.content, "done": False}
            if chunk.usage:
                yield {
                    "content": "",
                    "done": True,
                    "input_tokens": chunk.usage.prompt_tokens or 0,
                    "output_tokens": chunk.usage.completion_tokens or 0,
                }
