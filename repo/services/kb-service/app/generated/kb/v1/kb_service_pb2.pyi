import datetime

from common.v1 import common_pb2 as _common_pb2
from google.protobuf import empty_pb2 as _empty_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CreateKBRequest(_message.Message):
    __slots__ = ("tenant_id", "name", "description", "embedding_model", "chunk_size", "top_k", "score_threshold", "retrieval_mode")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    EMBEDDING_MODEL_FIELD_NUMBER: _ClassVar[int]
    CHUNK_SIZE_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    SCORE_THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    RETRIEVAL_MODE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    name: str
    description: str
    embedding_model: str
    chunk_size: int
    top_k: int
    score_threshold: float
    retrieval_mode: str
    def __init__(self, tenant_id: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., embedding_model: _Optional[str] = ..., chunk_size: _Optional[int] = ..., top_k: _Optional[int] = ..., score_threshold: _Optional[float] = ..., retrieval_mode: _Optional[str] = ...) -> None: ...

class GetKBRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ...) -> None: ...

class ListKBsRequest(_message.Message):
    __slots__ = ("tenant_id", "page")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    page: _common_pb2.CursorPageRequest
    def __init__(self, tenant_id: _Optional[str] = ..., page: _Optional[_Union[_common_pb2.CursorPageRequest, _Mapping]] = ...) -> None: ...

class ListKBsResponse(_message.Message):
    __slots__ = ("kbs", "meta")
    KBS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    kbs: _containers.RepeatedCompositeFieldContainer[KnowledgeBase]
    meta: _common_pb2.CursorPageMeta
    def __init__(self, kbs: _Optional[_Iterable[_Union[KnowledgeBase, _Mapping]]] = ..., meta: _Optional[_Union[_common_pb2.CursorPageMeta, _Mapping]] = ...) -> None: ...

class DeleteKBRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ...) -> None: ...

class UpdateKBRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "idempotency_key", "name", "description")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    idempotency_key: str
    name: str
    description: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class GetDocumentUploadURLRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "file_name", "file_type", "file_size_bytes", "checksum_sha256", "idempotency_key", "custom_metadata")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    FILE_TYPE_FIELD_NUMBER: _ClassVar[int]
    FILE_SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    CHECKSUM_SHA256_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    CUSTOM_METADATA_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    file_name: str
    file_type: str
    file_size_bytes: int
    checksum_sha256: str
    idempotency_key: str
    custom_metadata: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., file_name: _Optional[str] = ..., file_type: _Optional[str] = ..., file_size_bytes: _Optional[int] = ..., checksum_sha256: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., custom_metadata: _Optional[str] = ...) -> None: ...

class GetDocumentUploadURLResponse(_message.Message):
    __slots__ = ("doc_id", "upload_url", "storage_path")
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    UPLOAD_URL_FIELD_NUMBER: _ClassVar[int]
    STORAGE_PATH_FIELD_NUMBER: _ClassVar[int]
    doc_id: str
    upload_url: str
    storage_path: str
    def __init__(self, doc_id: _Optional[str] = ..., upload_url: _Optional[str] = ..., storage_path: _Optional[str] = ...) -> None: ...

class NotifyDocumentUploadedRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "doc_id", "storage_path")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    STORAGE_PATH_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    doc_id: str
    storage_path: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., doc_id: _Optional[str] = ..., storage_path: _Optional[str] = ...) -> None: ...

class GetDocumentRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "doc_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    doc_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., doc_id: _Optional[str] = ...) -> None: ...

class ListDocumentsRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "parse_status", "page")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    PARSE_STATUS_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    parse_status: str
    page: _common_pb2.CursorPageRequest
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., parse_status: _Optional[str] = ..., page: _Optional[_Union[_common_pb2.CursorPageRequest, _Mapping]] = ...) -> None: ...

class ListDocumentsResponse(_message.Message):
    __slots__ = ("documents", "meta")
    DOCUMENTS_FIELD_NUMBER: _ClassVar[int]
    META_FIELD_NUMBER: _ClassVar[int]
    documents: _containers.RepeatedCompositeFieldContainer[KBDocument]
    meta: _common_pb2.CursorPageMeta
    def __init__(self, documents: _Optional[_Iterable[_Union[KBDocument, _Mapping]]] = ..., meta: _Optional[_Union[_common_pb2.CursorPageMeta, _Mapping]] = ...) -> None: ...

class DeleteDocumentRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "doc_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    doc_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., doc_id: _Optional[str] = ...) -> None: ...

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

class RetrieveRequest(_message.Message):
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

class RetrieveEvent(_message.Message):
    __slots__ = ("token", "sources", "done", "error")
    TOKEN_FIELD_NUMBER: _ClassVar[int]
    SOURCES_FIELD_NUMBER: _ClassVar[int]
    DONE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    token: RetrieveTokenEvent
    sources: RetrieveSourcesEvent
    done: RetrieveDoneEvent
    error: RetrieveErrorEvent
    def __init__(self, token: _Optional[_Union[RetrieveTokenEvent, _Mapping]] = ..., sources: _Optional[_Union[RetrieveSourcesEvent, _Mapping]] = ..., done: _Optional[_Union[RetrieveDoneEvent, _Mapping]] = ..., error: _Optional[_Union[RetrieveErrorEvent, _Mapping]] = ...) -> None: ...

class RetrieveTokenEvent(_message.Message):
    __slots__ = ("content",)
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    content: str
    def __init__(self, content: _Optional[str] = ...) -> None: ...

class RetrieveSourcesEvent(_message.Message):
    __slots__ = ("sources",)
    SOURCES_FIELD_NUMBER: _ClassVar[int]
    sources: _containers.RepeatedCompositeFieldContainer[SourceChunk]
    def __init__(self, sources: _Optional[_Iterable[_Union[SourceChunk, _Mapping]]] = ...) -> None: ...

class RetrieveDoneEvent(_message.Message):
    __slots__ = ("input_tokens", "output_tokens", "session_id")
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    input_tokens: int
    output_tokens: int
    session_id: str
    def __init__(self, input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ..., session_id: _Optional[str] = ...) -> None: ...

class RetrieveErrorEvent(_message.Message):
    __slots__ = ("message", "code")
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CODE_FIELD_NUMBER: _ClassVar[int]
    message: str
    code: str
    def __init__(self, message: _Optional[str] = ..., code: _Optional[str] = ...) -> None: ...

class KnowledgeBase(_message.Message):
    __slots__ = ("tenant_id", "id", "name", "description", "embedding_model", "chunk_size", "top_k", "score_threshold", "retrieval_mode", "status", "doc_count", "created_at", "updated_at")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    EMBEDDING_MODEL_FIELD_NUMBER: _ClassVar[int]
    CHUNK_SIZE_FIELD_NUMBER: _ClassVar[int]
    TOP_K_FIELD_NUMBER: _ClassVar[int]
    SCORE_THRESHOLD_FIELD_NUMBER: _ClassVar[int]
    RETRIEVAL_MODE_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    DOC_COUNT_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    id: str
    name: str
    description: str
    embedding_model: str
    chunk_size: int
    top_k: int
    score_threshold: float
    retrieval_mode: str
    status: str
    doc_count: int
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, tenant_id: _Optional[str] = ..., id: _Optional[str] = ..., name: _Optional[str] = ..., description: _Optional[str] = ..., embedding_model: _Optional[str] = ..., chunk_size: _Optional[int] = ..., top_k: _Optional[int] = ..., score_threshold: _Optional[float] = ..., retrieval_mode: _Optional[str] = ..., status: _Optional[str] = ..., doc_count: _Optional[int] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class KBDocument(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "id", "file_name", "file_type", "file_size_bytes", "parse_status", "chunk_count", "error_message", "custom_metadata", "created_at", "parsed_at")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    FILE_TYPE_FIELD_NUMBER: _ClassVar[int]
    FILE_SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    PARSE_STATUS_FIELD_NUMBER: _ClassVar[int]
    CHUNK_COUNT_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CUSTOM_METADATA_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    PARSED_AT_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    id: str
    file_name: str
    file_type: str
    file_size_bytes: int
    parse_status: str
    chunk_count: int
    error_message: str
    custom_metadata: str
    created_at: _timestamp_pb2.Timestamp
    parsed_at: _timestamp_pb2.Timestamp
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., id: _Optional[str] = ..., file_name: _Optional[str] = ..., file_type: _Optional[str] = ..., file_size_bytes: _Optional[int] = ..., parse_status: _Optional[str] = ..., chunk_count: _Optional[int] = ..., error_message: _Optional[str] = ..., custom_metadata: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., parsed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ListKBCitationsRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "page")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    page: _common_pb2.CursorPageRequest
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., page: _Optional[_Union[_common_pb2.CursorPageRequest, _Mapping]] = ...) -> None: ...

class KBCitation(_message.Message):
    __slots__ = ("id", "kb_id", "doc_id", "file_name", "page", "content", "score", "created_at", "message_id", "session_id")
    ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    SCORE_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    kb_id: str
    doc_id: str
    file_name: str
    page: int
    content: str
    score: float
    created_at: _timestamp_pb2.Timestamp
    message_id: str
    session_id: str
    def __init__(self, id: _Optional[str] = ..., kb_id: _Optional[str] = ..., doc_id: _Optional[str] = ..., file_name: _Optional[str] = ..., page: _Optional[int] = ..., content: _Optional[str] = ..., score: _Optional[float] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., message_id: _Optional[str] = ..., session_id: _Optional[str] = ...) -> None: ...

class ListKBCitationsResponse(_message.Message):
    __slots__ = ("items", "next_cursor")
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    items: _containers.RepeatedCompositeFieldContainer[KBCitation]
    next_cursor: str
    def __init__(self, items: _Optional[_Iterable[_Union[KBCitation, _Mapping]]] = ..., next_cursor: _Optional[str] = ...) -> None: ...

class ListKBSessionsRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "page")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    page: _common_pb2.CursorPageRequest
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., page: _Optional[_Union[_common_pb2.CursorPageRequest, _Mapping]] = ...) -> None: ...

class KBSession(_message.Message):
    __slots__ = ("id", "kb_id", "message_count", "last_query", "created_at", "last_active_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    LAST_QUERY_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    LAST_ACTIVE_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    kb_id: str
    message_count: int
    last_query: str
    created_at: _timestamp_pb2.Timestamp
    last_active_at: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[str] = ..., kb_id: _Optional[str] = ..., message_count: _Optional[int] = ..., last_query: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., last_active_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ListKBSessionsResponse(_message.Message):
    __slots__ = ("items", "next_cursor")
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    items: _containers.RepeatedCompositeFieldContainer[KBSession]
    next_cursor: str
    def __init__(self, items: _Optional[_Iterable[_Union[KBSession, _Mapping]]] = ..., next_cursor: _Optional[str] = ...) -> None: ...

class UpdateKBPermissionsRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "idempotency_key", "public_read", "allowed_user_ids")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    PUBLIC_READ_FIELD_NUMBER: _ClassVar[int]
    ALLOWED_USER_IDS_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    idempotency_key: str
    public_read: bool
    allowed_user_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., public_read: _Optional[bool] = ..., allowed_user_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class KBChunk(_message.Message):
    __slots__ = ("id", "doc_id", "kb_id", "parent_chunk_id", "chunk_type", "content", "parent_content", "page_number", "content_type", "token_count", "custom_metadata", "created_at", "file_name")
    ID_FIELD_NUMBER: _ClassVar[int]
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    PARENT_CHUNK_ID_FIELD_NUMBER: _ClassVar[int]
    CHUNK_TYPE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    PARENT_CONTENT_FIELD_NUMBER: _ClassVar[int]
    PAGE_NUMBER_FIELD_NUMBER: _ClassVar[int]
    CONTENT_TYPE_FIELD_NUMBER: _ClassVar[int]
    TOKEN_COUNT_FIELD_NUMBER: _ClassVar[int]
    CUSTOM_METADATA_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    FILE_NAME_FIELD_NUMBER: _ClassVar[int]
    id: str
    doc_id: str
    kb_id: str
    parent_chunk_id: str
    chunk_type: str
    content: str
    parent_content: str
    page_number: int
    content_type: str
    token_count: int
    custom_metadata: str
    created_at: _timestamp_pb2.Timestamp
    file_name: str
    def __init__(self, id: _Optional[str] = ..., doc_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., parent_chunk_id: _Optional[str] = ..., chunk_type: _Optional[str] = ..., content: _Optional[str] = ..., parent_content: _Optional[str] = ..., page_number: _Optional[int] = ..., content_type: _Optional[str] = ..., token_count: _Optional[int] = ..., custom_metadata: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., file_name: _Optional[str] = ...) -> None: ...

class ListDocumentChunksRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "doc_id", "chunk_type", "page")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    CHUNK_TYPE_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    doc_id: str
    chunk_type: str
    page: _common_pb2.CursorPageRequest
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., doc_id: _Optional[str] = ..., chunk_type: _Optional[str] = ..., page: _Optional[_Union[_common_pb2.CursorPageRequest, _Mapping]] = ...) -> None: ...

class ListDocumentChunksResponse(_message.Message):
    __slots__ = ("items", "next_cursor")
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    items: _containers.RepeatedCompositeFieldContainer[KBChunk]
    next_cursor: str
    def __init__(self, items: _Optional[_Iterable[_Union[KBChunk, _Mapping]]] = ..., next_cursor: _Optional[str] = ...) -> None: ...

class KBSessionMessage(_message.Message):
    __slots__ = ("id", "session_id", "role", "content", "source_chunks", "input_tokens", "output_tokens", "duration_ms", "created_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    ROLE_FIELD_NUMBER: _ClassVar[int]
    CONTENT_FIELD_NUMBER: _ClassVar[int]
    SOURCE_CHUNKS_FIELD_NUMBER: _ClassVar[int]
    INPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_TOKENS_FIELD_NUMBER: _ClassVar[int]
    DURATION_MS_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    session_id: str
    role: str
    content: str
    source_chunks: str
    input_tokens: int
    output_tokens: int
    duration_ms: int
    created_at: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[str] = ..., session_id: _Optional[str] = ..., role: _Optional[str] = ..., content: _Optional[str] = ..., source_chunks: _Optional[str] = ..., input_tokens: _Optional[int] = ..., output_tokens: _Optional[int] = ..., duration_ms: _Optional[int] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class GetSessionMessagesRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "session_id", "page")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    session_id: str
    page: _common_pb2.CursorPageRequest
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., session_id: _Optional[str] = ..., page: _Optional[_Union[_common_pb2.CursorPageRequest, _Mapping]] = ...) -> None: ...

class GetSessionMessagesResponse(_message.Message):
    __slots__ = ("items", "next_cursor")
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    items: _containers.RepeatedCompositeFieldContainer[KBSessionMessage]
    next_cursor: str
    def __init__(self, items: _Optional[_Iterable[_Union[KBSessionMessage, _Mapping]]] = ..., next_cursor: _Optional[str] = ...) -> None: ...

class DeleteSessionRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "session_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    session_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., session_id: _Optional[str] = ...) -> None: ...

class ReparseDocumentRequest(_message.Message):
    __slots__ = ("tenant_id", "kb_id", "doc_id", "idempotency_key")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    KB_ID_FIELD_NUMBER: _ClassVar[int]
    DOC_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    kb_id: str
    doc_id: str
    idempotency_key: str
    def __init__(self, tenant_id: _Optional[str] = ..., kb_id: _Optional[str] = ..., doc_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ...) -> None: ...
