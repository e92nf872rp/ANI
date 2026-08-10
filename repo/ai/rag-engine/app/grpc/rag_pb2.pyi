from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class QueryRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "question", "session_id", "idempotency_key", "top_k", "score_threshold", "inference_service_name", "retrieval_mode")
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
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., question: _Optional[str] = ..., session_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., top_k: _Optional[int] = ..., score_threshold: _Optional[float] = ..., inference_service_name: _Optional[str] = ..., retrieval_mode: _Optional[str] = ...) -> None: ...

class QueryResponse(_message.Message):
    __slots__ = ("answer", "sources", "session_id", "input_tokens", "output_tokens")
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
    def __init__(self, answer: _Optional[str] = ..., sources: _Optional[_Iterable[_Union[SourceChunk, _Mapping]]] = ..., session_id: _Optional[str] = ..., input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ...) -> None: ...

class SourceChunk(_message.Message):
    __slots__ = ("doc_id", "file_name", "page", "content", "score")
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
    def __init__(self, doc_id: _Optional[str] = ..., file_name: _Optional[str] = ..., page: _Optional[int] = ..., content: _Optional[str] = ..., score: _Optional[float] = ...) -> None: ...
