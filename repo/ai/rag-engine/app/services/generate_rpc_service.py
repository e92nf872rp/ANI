"""Generate RPC service (RAG-REFACTOR-STEP-2 / Plan §2.4).

Stateless LLM generation using the pure Python ``openai`` SDK (no LlamaIndex
dependency). Reproduces the LlamaIndex ``ContextChatEngine`` +
``CompactAndRefine`` synthesizer behavior:

1. **Context repack** (reproduces ``CompactAndRefine._make_compact_text_chunks``):
   joins retrieved chunk texts and splits them into segments that each fit
   within the LLM context window (rough char-based estimate: 1 token ≈ 4
   chars). Each segment becomes a separate LLM call round.
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
# Rough chars-per-token estimate for context truncation (Plan §2.4).
CHARS_PER_TOKEN = 4
# Overhead chars reserved for system prompt + formatting beyond context_str.
CONTEXT_OVERHEAD_CHARS = 200


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


def _repack_context(context: list[dict], max_context_chars: int) -> list[str]:
    """Split context texts into segments fitting within max_context_chars.

    Reproduces LlamaIndex ``CompactAndRefine._make_compact_text_chunks`` /
    ``PromptHelper.repack`` behavior: each retrieved chunk's text is joined
    with ``\\n\\n`` and the combined text is split into segments of at most
    ``max_context_chars`` characters.

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
    if len(full_text) <= max_context_chars:
        return [full_text]
    # Split into segments of max_context_chars (rough truncation).
    segments: list[str] = []
    start = 0
    while start < len(full_text):
        segment = full_text[start : start + max_context_chars]
        segments.append(segment)
        start += max_context_chars
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

    def _max_context_chars(self) -> int:
        """Compute the max context chars per LLM call round.

        ``(context_window - max_tokens_reserve - overhead) * chars_per_token``
        """
        return max(
            1,
            (settings.vllm_context_window - 2048 - CONTEXT_OVERHEAD_CHARS)
            * CHARS_PER_TOKEN,
        )

    def _build_context_str(self, context: list[dict]) -> str:
        """Assemble + truncate context (single-segment shortcut).

        Kept for backward compatibility with tests. For the full multi-round
        CompactAndRefine behavior, ``_repack_context`` + ``generate`` should
        be used instead.
        """
        if not context:
            return ""
        context_str = "\n\n".join(c.get("content", "") for c in context)
        max_chars = self._max_context_chars()
        if len(context_str) > max_chars:
            context_str = context_str[:max_chars]
        return context_str

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
    ) -> tuple[str, int, int]:
        """Make a single LLM call and return (answer, input_tokens, output_tokens).

        Raises:
            TimeoutError: vLLM timed out.
            RuntimeError: vLLM unavailable / API error.
        """
        try:
            response = client.chat.completions.create(
                model=settings.vllm_model,
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
            inference_service_name: Reserved for per-request LLM routing
                (not yet implemented; the default model is always used).
            max_tokens: Max output tokens per round.

        Returns:
            ``{"answer", "input_tokens", "output_tokens", "session_id"}``.

        Raises:
            TimeoutError: vLLM timed out (→ gRPC DEADLINE_EXCEEDED).
            RuntimeError: vLLM unavailable / API error.
        """
        client = self._make_client()
        max_ctx_chars = self._max_context_chars()
        segments = _repack_context(context, max_ctx_chars)

        # No context → single call with empty context (matches old behavior).
        if not segments:
            messages = self._build_initial_messages(question, "", history)
            answer, input_tokens, output_tokens = self._call_llm(
                client, messages, max_tokens
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
            self._build_initial_messages(question, segments[0], history),
            max_tokens,
        )

        # Refine rounds: for each subsequent segment, refine the answer.
        for segment in segments[1:]:
            answer, in_tok, out_tok = self._call_llm(
                client,
                self._build_refine_messages(
                    question, segment, answer, history
                ),
                max_tokens,
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
        context_str = self._build_context_str(context)
        messages = self._build_initial_messages(question, context_str, history)
        client = self._make_client()
        try:
            stream = client.chat.completions.create(
                model=settings.vllm_model,
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
