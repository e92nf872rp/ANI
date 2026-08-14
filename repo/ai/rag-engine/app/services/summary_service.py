"""Document-level summary service (US-012 / SPEC §5.1).

``summary_service`` produces a document-level summary chunk from the parent
blocks emitted by ``chunk_service``:

1. Concatenate the first ``N`` parent blocks' full text (SPEC §5.1).
2. Ask the LLM to summarise the combined text into a 200-500 character
   summary (SPEC §5.1).
3. Wrap the summary in a :class:`ChildChunk` (``content_type='text'``) so
   :func:`embed_service.EmbedService.embed_and_write` can embed it and
   persist it to the KB's Milvus collection with
   ``chunk_type='doc_summary'`` (SPEC §3.1, §5.1).

Per SPEC §5.1 / §5.4 and PRD US-012 AC7, summary generation is best-effort:
on any failure (LLM timeout, empty result, exception) the service logs a
warning and returns ``None`` so the caller (``parse_worker``) degrades to
parent-child chunking only and ingestion is NOT blocked (SPEC §6.3).

The LLM is injected via the ``llm`` constructor argument (protocol-typed)
so the service is unit-testable without a live vLLM endpoint. The default
factory builds a LlamaIndex :class:`OpenAILike` client pointed at the vLLM
endpoint configured in ``settings`` (``vllm_*`` fields in ``config.py``).
"""
from __future__ import annotations

import logging
import uuid
from collections.abc import Callable
from typing import Any, Protocol

from app.services.chunk_service import ChildChunk, ParentChunk

logger = logging.getLogger(__name__)

# ── SPEC §5.1 parameters ─────────────────────────────────────────────────────
# Number of leading parent blocks to concatenate as the LLM input context.
DEFAULT_SUMMARY_PARENT_COUNT = 3
# Summary length bounds in characters (PRD US-012 / SPEC §5.1: "200-500 字").
SUMMARY_MIN_CHARS = 200
SUMMARY_MAX_CHARS = 500
# The summary chunk is a logical document-level node; page_number is unknown.
SUMMARY_PAGE_NUMBER = 1


class _LLM(Protocol):
    """Minimal LLM surface used by :class:`SummaryService` (LlamaIndex-compatible)."""

    def complete(self, prompt: str) -> Any: ...


# ── Prompt construction ──────────────────────────────────────────────────────

_SUMMARY_PROMPT_TEMPLATE = (
    "请总结以下内容为 {lo}-{hi} 字的摘要：\n{content}"
)


def _build_prompt(content: str) -> str:
    """Build the summary prompt (SPEC §5.1)."""
    return _SUMMARY_PROMPT_TEMPLATE.format(
        lo=SUMMARY_MIN_CHARS, hi=SUMMARY_MAX_CHARS, content=content
    )


def _concat_parents(parents: list[ParentChunk], n: int) -> str:
    """Concatenate the first ``n`` parent blocks' full text (SPEC §5.1).

    Uses ``ParentChunk.content`` (the full text of each parent block, already
    the concatenation of its child contents — see ``chunk_service``). Joins
    with a blank separator. Truncates to ``n`` defensively even if the caller
    passes more parents than requested.
    """
    selected = parents[:n]
    return "\n".join(p.content for p in selected if p.content).strip()


def _extract_summary_text(raw: Any) -> str:
    """Extract the summary text from an LLM completion.

    LlamaIndex's ``LLM.complete`` returns a ``CompletionResponse`` whose
    ``.text`` attribute carries the generated string. To stay robust against
    both real LlamaIndex responses and simple mocks (which may return a bare
    string), this helper accepts:

    * a string (used directly),
    * an object exposing ``.text`` (LlamaIndex ``CompletionResponse``),
    * an object exposing ``.response`` (some LLM adapters).
    """
    if isinstance(raw, str):
        return raw
    for attr in ("text", "response"):
        val = getattr(raw, attr, None)
        if isinstance(val, str):
            return val
    # Last resort: stringify so the caller can still log/inspect it.
    return str(raw) if raw is not None else ""


def _validate_summary(summary: str) -> str | None:
    """Validate the generated summary (SPEC §5.1: 200-500 字).

    The 200-500 character target is enforced via the LLM prompt
    (``_SUMMARY_PROMPT_TEMPLATE``); here we only reject empty summaries.
    Summaries outside the target length are still persisted so the document
    has a summary node — dropping them would lose the summary entirely and
    only the prompt guides the length. Whitespace is stripped before the
    empty check so blank-only output is caught.
    """
    summary = summary.strip()
    if not summary:
        return None
    if len(summary) < SUMMARY_MIN_CHARS or len(summary) > SUMMARY_MAX_CHARS:
        logger.debug(
            "summary_service: summary len=%d outside target [%d, %d] (accepted anyway; "
            "length is prompt-guided)",
            len(summary),
            SUMMARY_MIN_CHARS,
            SUMMARY_MAX_CHARS,
        )
    return summary


# ── Default LLM factory (LlamaIndex OpenAILike pointed at vLLM) ───────────────


def _default_llm_factory() -> _LLM:
    """Build the default LlamaIndex :class:`OpenAILike` LLM from settings.

    Reads vLLM connection settings from ``settings`` (``vllm_model``,
    ``vllm_api_base``, ``vllm_api_key``, ``vllm_context_window`` — defined in
    ``config.py``). When the vLLM settings are absent the factory raises
    ``RuntimeError`` — callers (``parse_worker``) must inject an LLM in
    environments where vLLM is unavailable (e.g. unit tests), and the
    degradation path turns the error into a warning + ``None``.
    """
    from app.core.config import settings

    model = settings.vllm_model
    api_base = settings.vllm_api_base
    api_key = settings.vllm_api_key or "EMPTY"
    if not model or not api_base:
        raise RuntimeError(
            "vLLM model/api_base not configured; set settings.vllm_model and "
            "settings.vllm_api_base (or inject an llm into SummaryService)"
        )
    from llama_index.llms.openai_like import OpenAILike

    return OpenAILike(
        model=model,
        api_base=api_base,
        api_key=api_key,
        is_chat_model=True,
        context_window=settings.vllm_context_window,
    )


# ── SummaryService ──────────────────────────────────────────────────────────


class SummaryService:
    """Document-level summary generator (SPEC §5.1, US-012).

    Args:
        llm: LLM client (LlamaIndex ``OpenAILike``-compatible). When ``None``
            the default factory is invoked lazily on first ``summarize`` call
            so the service can be constructed in environments without a live
            vLLM endpoint (tests inject a mock ``llm``).
        parent_count: Number of leading parent blocks to concatenate as the
            LLM input (default :data:`DEFAULT_SUMMARY_PARENT_COUNT`).
        llm_factory: Override the default LLM factory (used by tests).
    """

    def __init__(
        self,
        *,
        llm: _LLM | None = None,
        parent_count: int = DEFAULT_SUMMARY_PARENT_COUNT,
        llm_factory: Callable[[], _LLM] = _default_llm_factory,
    ) -> None:
        if parent_count < 1:
            raise ValueError(f"parent_count must be >= 1; got {parent_count}")
        self._llm = llm
        self._parent_count = parent_count
        self._llm_factory = llm_factory

    def summarize(self, parents: list[ParentChunk]) -> ChildChunk | None:
        """Generate a document-level summary chunk from parent blocks.

        Flow (SPEC §5.1)::

            first_n_parents = parents[:N]
            combined = "\n".join(p.content for p in first_n_parents)
            summary = llm.complete("总结以下内容为 200-500 字摘要：\n{combined}")
            # failure → degrade (warning), do not block ingestion

        Returns:
            A :class:`ChildChunk` carrying the summary text, ready to be
            passed to :func:`embed_service.EmbedService.embed_and_write` as
            ``summaries=[...]`` (Index layer embeds + stores with
            ``chunk_type='doc_summary'``). On any failure the service logs a
            warning and returns ``None`` so the caller degrades to parent-
            child chunking only (SPEC §5.4, §6.3, PRD US-012 AC7).

        Returns ``None`` immediately when there are no parents (nothing to
        summarise) — this is not an error and is logged at debug level.
        """
        if not parents:
            logger.debug("summary_service: no parents, skipping summary")
            return None

        combined = _concat_parents(parents, self._parent_count)
        if not combined:
            logger.warning("summary_service: concatenated parents empty, skipping")
            return None

        llm = self._llm
        if llm is None:
            try:
                # Lazily build once and cache on self._llm so the long-lived
                # service reuses one OpenAILike client across many documents.
                llm = self._llm = self._llm_factory()
            except Exception as exc:  # noqa: BLE001 — factory failure path (covered by tests)
                logger.warning("summary_service: LLM factory failed: %s; degrading", exc)
                return None

        prompt = _build_prompt(combined)
        try:
            raw = llm.complete(prompt)
        except Exception as exc:  # noqa: BLE001 — degrade to parent-child only
            logger.warning(
                "summary_service: LLM complete failed (%s); degrading to parent-child only",
                exc,
            )
            return None

        summary = _extract_summary_text(raw)
        validated = _validate_summary(summary)
        if validated is None:
            logger.warning(
                "summary_service: summary empty; degrading to parent-child only"
            )
            return None

        return ChildChunk(
            chunk_id=str(uuid.uuid4()),
            content=validated,
            content_type="text",
            page_number=SUMMARY_PAGE_NUMBER,
            token_count=max(1, len(validated) // 2),
        )


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit("summary_service is a library module; import from parse_worker")
