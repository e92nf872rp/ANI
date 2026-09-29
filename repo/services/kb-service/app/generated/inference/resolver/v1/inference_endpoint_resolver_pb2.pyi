from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class ResolveInternalEndpointRequest(_message.Message):
    __slots__ = ("tenant_id", "service_id", "served_model_name")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    SERVED_MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    service_id: str
    served_model_name: str
    def __init__(self, tenant_id: _Optional[str] = ..., service_id: _Optional[str] = ..., served_model_name: _Optional[str] = ...) -> None: ...

class ResolveInternalEndpointResponse(_message.Message):
    __slots__ = ("tenant_id", "service_id", "base_url", "served_model_name", "status", "task")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    SERVED_MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    TASK_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    service_id: str
    base_url: str
    served_model_name: str
    status: str
    task: str
    def __init__(self, tenant_id: _Optional[str] = ..., service_id: _Optional[str] = ..., base_url: _Optional[str] = ..., served_model_name: _Optional[str] = ..., status: _Optional[str] = ..., task: _Optional[str] = ...) -> None: ...
