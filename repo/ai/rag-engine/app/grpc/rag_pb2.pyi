from collections.abc import Iterable as _Iterable
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar

from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from google.protobuf.internal import containers as _containers

DESCRIPTOR: _descriptor.FileDescriptor

class QueryRequest(_message.Message):
    __slots__ = ("idempotency_key", "inference_service_name", "kb_id", "question", "retrieval_mode", "score_threshold", "session_id", "tenant_id", "top_k")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    QUESTION_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    SCORE_THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_NAME_FIELD_NUMBER: _ClassVar[int]
    RETRIEVAL_MODE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    question: str
    session_id: str
    idempotency_key: str
    top_k: int
    score_threshold: float
    inference_service_name: str
    retrieval_mode: str
    def __init__(self, tenant_id: str | None = ..., kb_id: str | None = ..., question: str | None = ..., session_id: str | None = ..., idempotency_key: str | None = ..., top_k: int | None = ..., score_threshold: float | None = ..., inference_service_name: str | None = ..., retrieval_mode: str | None = ...) -> None: ...

class QueryResponse(_message.Message):
    __slots__ = ("answer", "input_tokens", "output_tokens", "session_id", "sources")
    ANSWER_FIELD_NUMBER: _ClassVar[int]
    SOURCES_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    answer: str
    sources: _containers.RepeatedCompositeFieldContainer[SourceChunk]
    session_id: str
    input_tokens: int
    output_tokens: int
    def __init__(self, answer: str | None = ..., sources: _Iterable[SourceChunk | _Mapping] | None = ..., session_id: str | None = ..., input_tokens: int | None = ..., output_tokens: int | None = ...) -> None: ...

class SourceChunk(_message.Message):
    __slots__ = ("content", "doc_id", "file_name", "page", "score")
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    doc_id: str
    file_name: str
    page: int
    content: str
    score: float
    def __init__(self, doc_id: str | None = ..., file_name: str | None = ..., page: int | None = ..., content: str | None = ..., score: float | None = ...) -> None: ...
