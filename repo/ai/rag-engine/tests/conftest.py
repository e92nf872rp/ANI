"""Shared pytest fixtures and stub configuration for rag-engine tests.

This conftest stubs heavy/optional dependencies (llama_index, docling, grpc,
etc.) so that all test modules import cleanly without requiring the real
packages to be installed. Individual test files that need additional stubs
or fake class configurations can still add them on top of these base stubs.
"""
from __future__ import annotations

import os
import sys
from typing import Any
from unittest.mock import MagicMock

# ── sys.path: make rag-engine root importable (app.* imports) ────────────────
_HERE = os.path.dirname(os.path.abspath(__file__))
_REPO_ROOT = os.path.dirname(os.path.dirname(_HERE))
if _REPO_ROOT not in sys.path:
    sys.path.insert(0, os.path.join(_REPO_ROOT, "ai", "rag-engine"))

# ── Stub heavy/optional deps so modules import cleanly ────────────────────────
for _mod in (
    "docling",
    "docling.datamodel.base_models",
    "docling.datamodel.pipeline_options",
    "docling.document_converter",
    "docling_core",
    "docling_core.types.doc",
    "llama_index",
    "llama_index.readers",
    "llama_index.readers.docling",
    "llama_index.core",
    "llama_index.core.node_parser",
    "llama_index.core.schema",
    "llama_index.core.retrievers",
    "llama_index.core.chat_engine",
    "llama_index.core.memory",
    "llama_index.embeddings",
    "openai",
    "fitz",
    "docx",
    "openpyxl",
    "pptx",
    "grpc",
    "grpc.aio",
    "grpc._utilities",
):
    if _mod not in sys.modules:
        sys.modules[_mod] = MagicMock()

# ── gRPC stub version patches (rag_pb2_grpc reads __version__) ───────────────
_grpc_stub: Any = sys.modules["grpc"]
_grpc_stub.__version__ = "1.83.0"
_grpc_utilities_stub: Any = sys.modules["grpc._utilities"]
_grpc_utilities_stub.first_version_is_lower = lambda a, b: False
