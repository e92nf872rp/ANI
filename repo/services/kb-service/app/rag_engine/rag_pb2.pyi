from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class SourceChunk(_message.Message):
    __slots__ = ("chunk_id", "doc_id", "file_name", "page", "content", "score")
    CHUNK_ID_FIELD_NUMBER: _ClassVar[int]
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    chunk_id: str
    doc_id: str
    file_name: str
    page: int
    content: str
    score: float
    def __init__(self, chunk_id: _Optional[str] = ..., doc_id: _Optional[str] = ..., file_name: _Optional[str] = ..., page: _Optional[int] = ..., content: _Optional[str] = ..., score: _Optional[float] = ...) -> None: ...

class ParseRequest(_message.Message):
    __slots__ = ("download_url", "file_name", "file_type", "chunk_size")
    DOWNLOAD_URL_FIELD_NUMBER: _ClassVar[int]
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    FILE_TYPE_FIELD_NUMBER: _ClassVar[int]
    CHUNK_SIZE_FIELD_NUMBER: _ClassVar[int]
    download_url: str
    file_name: str
    file_type: str
    chunk_size: int
    def __init__(self, download_url: _Optional[str] = ..., file_name: _Optional[str] = ..., file_type: _Optional[str] = ..., chunk_size: _Optional[int] = ...) -> None: ...

class ParsedChunk(_message.Message):
    __slots__ = ("chunk_id", "content", "content_type", "page_number", "parent_content", "chunk_type", "metadata_json", "image_bytes", "image_format", "parent_chunk_id")
    CHUNK_ID_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    PAGE_NUMBER_FIELD_NUMBER: _ClassVar[int]
    PARENT_CONTENT_FIELD_NUMBER: _ClassVar[int]
    CHUNK_TYPE_FIELD_NUMBER: _ClassVar[int]
    METADATA_JSON_FIELD_NUMBER: _ClassVar[int]
    IMAGE_BYTES_FIELD_NUMBER: _ClassVar[int]
    IMAGE_FORMAT_FIELD_NUMBER: _ClassVar[int]
    PARENT_CHUNK_ID_FIELD_NUMBER: _ClassVar[int]
    chunk_id: str
    content: str
    content_type: str
    page_number: int
    parent_content: str
    chunk_type: str
    metadata_json: str
    image_bytes: bytes
    image_format: str
    parent_chunk_id: str
    def __init__(self, chunk_id: _Optional[str] = ..., content: _Optional[str] = ..., content_type: _Optional[str] = ..., page_number: _Optional[int] = ..., parent_content: _Optional[str] = ..., chunk_type: _Optional[str] = ..., metadata_json: _Optional[str] = ..., image_bytes: _Optional[bytes] = ..., image_format: _Optional[str] = ..., parent_chunk_id: _Optional[str] = ...) -> None: ...

class ParseResponse(_message.Message):
    __slots__ = ("chunks",)
    CHUNKS_FIELD_NUMBER: _ClassVar[int]
    chunks: _containers.RepeatedCompositeFieldContainer[ParsedChunk]
    def __init__(self, chunks: _Optional[_Iterable[_Union[ParsedChunk, _Mapping]]] = ...) -> None: ...

class EmbedRequest(_message.Message):
    __slots__ = ("texts", "model")
    TEXTS_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    texts: _containers.RepeatedScalarFieldContainer[str]
    model: str
    def __init__(self, texts: _Optional[_Iterable[str]] = ..., model: _Optional[str] = ...) -> None: ...

class EmbedResponse(_message.Message):
    __slots__ = ("vectors_flat", "dimension", "count")
    VECTORS_FLAT_FIELD_NUMBER: _ClassVar[int]
    DIMENSION_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    vectors_flat: _containers.RepeatedScalarFieldContainer[float]
    dimension: int
    count: int
    def __init__(self, vectors_flat: _Optional[_Iterable[float]] = ..., dimension: _Optional[int] = ..., count: _Optional[int] = ...) -> None: ...

class GenerateRequest(_message.Message):
    __slots__ = ("question", "session_id", "context", "inference_service_name", "max_tokens", "history")
    QUESTION_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    CONTEXT_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_NAME_FIELD_NUMBER: _ClassVar[int]
    MAX_TOKENS_FIELD_NUMBER: _ClassVar[int]
    HISTORY_FIELD_NUMBER: _ClassVar[int]
    question: str
    session_id: str
    context: _containers.RepeatedCompositeFieldContainer[SourceChunk]
    inference_service_name: str
    max_tokens: int
    history: _containers.RepeatedCompositeFieldContainer[ChatMessage]
    def __init__(self, question: _Optional[str] = ..., session_id: _Optional[str] = ..., context: _Optional[_Iterable[_Union[SourceChunk, _Mapping]]] = ..., inference_service_name: _Optional[str] = ..., max_tokens: _Optional[int] = ..., history: _Optional[_Iterable[_Union[ChatMessage, _Mapping]]] = ...) -> None: ...

class ChatMessage(_message.Message):
    __slots__ = ("role", "content")
    ROLE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    role: str
    content: str
    def __init__(self, role: _Optional[str] = ..., content: _Optional[str] = ...) -> None: ...

class GenerateResponse(_message.Message):
    __slots__ = ("answer", "input_tokens", "output_tokens", "session_id")
    ANSWER_FIELD_NUMBER: _ClassVar[int]
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    answer: str
    input_tokens: int
    output_tokens: int
    session_id: str
    def __init__(self, answer: _Optional[str] = ..., input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ..., session_id: _Optional[str] = ...) -> None: ...

class GenerateToken(_message.Message):
    __slots__ = ("content", "done", "input_tokens", "output_tokens")
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    DONE_FIELD_NUMBER: _ClassVar[int]
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    content: str
    done: bool
    input_tokens: int
    output_tokens: int
    def __init__(self, content: _Optional[str] = ..., done: _Optional[bool] = ..., input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ...) -> None: ...
