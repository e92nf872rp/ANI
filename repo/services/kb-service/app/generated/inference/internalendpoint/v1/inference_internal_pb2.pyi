from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class ResolveInternalEndpointRequest(_message.Message):
    __slots__ = ("tenant_id", "served_model_name", "service_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVED_MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    served_model_name: str
    service_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., served_model_name: _Optional[str] = ..., service_id: _Optional[str] = ...) -> None: ...

class ResolveInternalEndpointResponse(_message.Message):
    __slots__ = ("base_url", "served_model_name", "task", "status")
    BASE_URL_FIELD_NUMBER: _ClassVar[int]
    SERVED_MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    TASK_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    base_url: str
    served_model_name: str
    task: str
    status: str
    def __init__(self, base_url: _Optional[str] = ..., served_model_name: _Optional[str] = ..., task: _Optional[str] = ..., status: _Optional[str] = ...) -> None: ...
