from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class ResourceType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RESOURCE_TYPE_UNSPECIFIED: _ClassVar[ResourceType]
    RESOURCE_TYPE_GPU_HOURS: _ClassVar[ResourceType]
    RESOURCE_TYPE_CPU_HOURS: _ClassVar[ResourceType]
    RESOURCE_TYPE_MEMORY_GB_HOURS: _ClassVar[ResourceType]
    RESOURCE_TYPE_STORAGE_GB_DAYS: _ClassVar[ResourceType]
    RESOURCE_TYPE_INPUT_TOKENS: _ClassVar[ResourceType]
    RESOURCE_TYPE_OUTPUT_TOKENS: _ClassVar[ResourceType]
    RESOURCE_TYPE_KB_QUERIES: _ClassVar[ResourceType]
RESOURCE_TYPE_UNSPECIFIED: ResourceType
RESOURCE_TYPE_GPU_HOURS: ResourceType
RESOURCE_TYPE_CPU_HOURS: ResourceType
RESOURCE_TYPE_MEMORY_GB_HOURS: ResourceType
RESOURCE_TYPE_STORAGE_GB_DAYS: ResourceType
RESOURCE_TYPE_INPUT_TOKENS: ResourceType
RESOURCE_TYPE_OUTPUT_TOKENS: ResourceType
RESOURCE_TYPE_KB_QUERIES: ResourceType

class TenantContext(_message.Message):
    __slots__ = ("tenant_id", "user_id", "roles", "scope")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    ROLES_FIELD_NUMBER: _ClassVar[int]
    SCOPE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    user_id: str
    roles: _containers.RepeatedScalarFieldContainer[str]
    scope: str
    def __init__(self, tenant_id: _Optional[str] = ..., user_id: _Optional[str] = ..., roles: _Optional[_Iterable[str]] = ..., scope: _Optional[str] = ...) -> None: ...

class CursorPageRequest(_message.Message):
    __slots__ = ("limit", "cursor")
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    CURSOR_FIELD_NUMBER: _ClassVar[int]
    limit: int
    cursor: str
    def __init__(self, limit: _Optional[int] = ..., cursor: _Optional[str] = ...) -> None: ...

class CursorPageMeta(_message.Message):
    __slots__ = ("total", "next_cursor")
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    total: int
    next_cursor: str
    def __init__(self, total: _Optional[int] = ..., next_cursor: _Optional[str] = ...) -> None: ...

class AsyncTaskRef(_message.Message):
    __slots__ = ("task_id", "task_type", "status", "location_url")
    TASK_ID_FIELD_NUMBER: _ClassVar[int]
    TASK_TYPE_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    LOCATION_URL_FIELD_NUMBER: _ClassVar[int]
    task_id: str
    task_type: str
    status: str
    location_url: str
    def __init__(self, task_id: _Optional[str] = ..., task_type: _Optional[str] = ..., status: _Optional[str] = ..., location_url: _Optional[str] = ...) -> None: ...
