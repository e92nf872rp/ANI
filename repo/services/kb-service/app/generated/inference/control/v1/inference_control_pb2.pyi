import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ListInferenceServicesRequest(_message.Message):
    __slots__ = ("tenant_id",)
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    def __init__(self, tenant_id: _Optional[str] = ...) -> None: ...

class ListInferenceServicesResponse(_message.Message):
    __slots__ = ("items",)
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    items: _containers.RepeatedCompositeFieldContainer[InferenceService]
    def __init__(self, items: _Optional[_Iterable[_Union[InferenceService, _Mapping]]] = ...) -> None: ...

class CreateInferenceServiceRequest(_message.Message):
    __slots__ = ("tenant_id", "idempotency_key", "name", "model", "model_version_id", "served_model_name", "replicas", "resources", "placement_mode", "gpu_type", "gpu_count_per_pod", "image_id", "image_ref", "engine")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    MODEL_VERSION_ID_FIELD_NUMBER: _ClassVar[int]
    SERVED_MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    REPLICAS_FIELD_NUMBER: _ClassVar[int]
    RESOURCES_FIELD_NUMBER: _ClassVar[int]
    PLACEMENT_MODE_FIELD_NUMBER: _ClassVar[int]
    GPU_TYPE_FIELD_NUMBER: _ClassVar[int]
    GPU_COUNT_PER_POD_FIELD_NUMBER: _ClassVar[int]
    IMAGE_ID_FIELD_NUMBER: _ClassVar[int]
    IMAGE_REF_FIELD_NUMBER: _ClassVar[int]
    ENGINE_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    idempotency_key: str
    name: str
    model: str
    model_version_id: str
    served_model_name: str
    replicas: int
    resources: InferenceServiceResources
    placement_mode: str
    gpu_type: str
    gpu_count_per_pod: int
    image_id: str
    image_ref: str
    engine: InferenceServiceEngine
    def __init__(self, tenant_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., name: _Optional[str] = ..., model: _Optional[str] = ..., model_version_id: _Optional[str] = ..., served_model_name: _Optional[str] = ..., replicas: _Optional[int] = ..., resources: _Optional[_Union[InferenceServiceResources, _Mapping]] = ..., placement_mode: _Optional[str] = ..., gpu_type: _Optional[str] = ..., gpu_count_per_pod: _Optional[int] = ..., image_id: _Optional[str] = ..., image_ref: _Optional[str] = ..., engine: _Optional[_Union[InferenceServiceEngine, _Mapping]] = ...) -> None: ...

class GetInferenceServiceRequest(_message.Message):
    __slots__ = ("tenant_id", "service_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    service_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., service_id: _Optional[str] = ...) -> None: ...

class ScaleInferenceServiceRequest(_message.Message):
    __slots__ = ("tenant_id", "service_id", "idempotency_key", "replicas")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    REPLICAS_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    service_id: str
    idempotency_key: str
    replicas: int
    def __init__(self, tenant_id: _Optional[str] = ..., service_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., replicas: _Optional[int] = ...) -> None: ...

class DeleteInferenceServiceRequest(_message.Message):
    __slots__ = ("tenant_id", "service_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    service_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., service_id: _Optional[str] = ...) -> None: ...

class ApplyInferenceServiceLifecycleRequest(_message.Message):
    __slots__ = ("tenant_id", "service_id", "idempotency_key", "action")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    ACTION_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    service_id: str
    idempotency_key: str
    action: str
    def __init__(self, tenant_id: _Optional[str] = ..., service_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., action: _Optional[str] = ...) -> None: ...

class GetInferenceOperationRequest(_message.Message):
    __slots__ = ("tenant_id", "operation_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    OPERATION_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    operation_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., operation_id: _Optional[str] = ...) -> None: ...

class ListInferenceServiceLogsRequest(_message.Message):
    __slots__ = ("tenant_id", "service_id", "limit", "cursor", "level")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    CURSOR_FIELD_NUMBER: _ClassVar[int]
    LEVEL_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    service_id: str
    limit: int
    cursor: str
    level: str
    def __init__(self, tenant_id: _Optional[str] = ..., service_id: _Optional[str] = ..., limit: _Optional[int] = ..., cursor: _Optional[str] = ..., level: _Optional[str] = ...) -> None: ...

class ListInferenceServiceLogsResponse(_message.Message):
    __slots__ = ("items", "next_cursor")
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    items: _containers.RepeatedCompositeFieldContainer[InferenceServiceLogEntry]
    next_cursor: str
    def __init__(self, items: _Optional[_Iterable[_Union[InferenceServiceLogEntry, _Mapping]]] = ..., next_cursor: _Optional[str] = ...) -> None: ...

class InferenceServiceLogEntry(_message.Message):
    __slots__ = ("timestamp", "level", "message", "container", "stream")
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    LEVEL_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CONTAINER_FIELD_NUMBER: _ClassVar[int]
    STREAM_FIELD_NUMBER: _ClassVar[int]
    timestamp: _timestamp_pb2.Timestamp
    level: str
    message: str
    container: str
    stream: str
    def __init__(self, timestamp: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., level: _Optional[str] = ..., message: _Optional[str] = ..., container: _Optional[str] = ..., stream: _Optional[str] = ...) -> None: ...

class InferenceService(_message.Message):
    __slots__ = ("id", "name", "model", "model_version_id", "served_model_name", "replicas", "ready_replicas", "resources", "placement_mode", "gpu_type", "gpu_count_per_pod", "max_concurrency", "status", "status_reason", "status_message", "generation", "observed_generation", "current_operation_id", "created_at", "updated_at", "image_id", "image_ref", "engine", "invocation_url")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    MODEL_FIELD_NUMBER: _ClassVar[int]
    MODEL_VERSION_ID_FIELD_NUMBER: _ClassVar[int]
    SERVED_MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    REPLICAS_FIELD_NUMBER: _ClassVar[int]
    READY_REPLICAS_FIELD_NUMBER: _ClassVar[int]
    RESOURCES_FIELD_NUMBER: _ClassVar[int]
    PLACEMENT_MODE_FIELD_NUMBER: _ClassVar[int]
    GPU_TYPE_FIELD_NUMBER: _ClassVar[int]
    GPU_COUNT_PER_POD_FIELD_NUMBER: _ClassVar[int]
    MAX_CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    STATUS_REASON_FIELD_NUMBER: _ClassVar[int]
    STATUS_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    GENERATION_FIELD_NUMBER: _ClassVar[int]
    OBSERVED_GENERATION_FIELD_NUMBER: _ClassVar[int]
    CURRENT_OPERATION_ID_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    IMAGE_ID_FIELD_NUMBER: _ClassVar[int]
    IMAGE_REF_FIELD_NUMBER: _ClassVar[int]
    ENGINE_FIELD_NUMBER: _ClassVar[int]
    INVOCATION_URL_FIELD_NUMBER: _ClassVar[int]
    id: str
    name: str
    model: str
    model_version_id: str
    served_model_name: str
    replicas: int
    ready_replicas: int
    resources: InferenceServiceResources
    placement_mode: str
    gpu_type: str
    gpu_count_per_pod: int
    max_concurrency: int
    status: str
    status_reason: str
    status_message: str
    generation: int
    observed_generation: int
    current_operation_id: str
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    image_id: str
    image_ref: str
    engine: InferenceServiceEngine
    invocation_url: str
    def __init__(self, id: _Optional[str] = ..., name: _Optional[str] = ..., model: _Optional[str] = ..., model_version_id: _Optional[str] = ..., served_model_name: _Optional[str] = ..., replicas: _Optional[int] = ..., ready_replicas: _Optional[int] = ..., resources: _Optional[_Union[InferenceServiceResources, _Mapping]] = ..., placement_mode: _Optional[str] = ..., gpu_type: _Optional[str] = ..., gpu_count_per_pod: _Optional[int] = ..., max_concurrency: _Optional[int] = ..., status: _Optional[str] = ..., status_reason: _Optional[str] = ..., status_message: _Optional[str] = ..., generation: _Optional[int] = ..., observed_generation: _Optional[int] = ..., current_operation_id: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., image_id: _Optional[str] = ..., image_ref: _Optional[str] = ..., engine: _Optional[_Union[InferenceServiceEngine, _Mapping]] = ..., invocation_url: _Optional[str] = ...) -> None: ...

class InferenceServiceEngine(_message.Message):
    __slots__ = ("env", "command")
    ENV_FIELD_NUMBER: _ClassVar[int]
    COMMAND_FIELD_NUMBER: _ClassVar[int]
    env: _containers.RepeatedCompositeFieldContainer[InferenceServiceEngineEnvVar]
    command: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, env: _Optional[_Iterable[_Union[InferenceServiceEngineEnvVar, _Mapping]]] = ..., command: _Optional[_Iterable[str]] = ...) -> None: ...

class InferenceServiceEngineEnvVar(_message.Message):
    __slots__ = ("name", "value")
    NAME_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    name: str
    value: str
    def __init__(self, name: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...

class InferenceServiceResources(_message.Message):
    __slots__ = ("cpu", "memory", "accelerator")
    CPU_FIELD_NUMBER: _ClassVar[int]
    MEMORY_FIELD_NUMBER: _ClassVar[int]
    ACCELERATOR_FIELD_NUMBER: _ClassVar[int]
    cpu: str
    memory: str
    accelerator: InferenceServiceAccelerator
    def __init__(self, cpu: _Optional[str] = ..., memory: _Optional[str] = ..., accelerator: _Optional[_Union[InferenceServiceAccelerator, _Mapping]] = ...) -> None: ...

class InferenceServiceAccelerator(_message.Message):
    __slots__ = ("spec_id", "count_per_replica", "memory")
    SPEC_ID_FIELD_NUMBER: _ClassVar[int]
    COUNT_PER_REPLICA_FIELD_NUMBER: _ClassVar[int]
    MEMORY_FIELD_NUMBER: _ClassVar[int]
    spec_id: str
    count_per_replica: int
    memory: int
    def __init__(self, spec_id: _Optional[str] = ..., count_per_replica: _Optional[int] = ..., memory: _Optional[int] = ...) -> None: ...

class InferenceOperation(_message.Message):
    __slots__ = ("id", "task_type", "resource_type", "resource_id", "idempotency_key", "status", "attempt_count", "progress_pct", "error_message", "created_at", "completed_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    TASK_TYPE_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_TYPE_FIELD_NUMBER: _ClassVar[int]
    RESOURCE_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    ATTEMPT_COUNT_FIELD_NUMBER: _ClassVar[int]
    PROGRESS_PCT_FIELD_NUMBER: _ClassVar[int]
    ERROR_MESSAGE_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    COMPLETED_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    task_type: str
    resource_type: str
    resource_id: str
    idempotency_key: str
    status: str
    attempt_count: int
    progress_pct: int
    error_message: str
    created_at: _timestamp_pb2.Timestamp
    completed_at: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[str] = ..., task_type: _Optional[str] = ..., resource_type: _Optional[str] = ..., resource_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., status: _Optional[str] = ..., attempt_count: _Optional[int] = ..., progress_pct: _Optional[int] = ..., error_message: _Optional[str] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., completed_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class InferenceAccessPolicy(_message.Message):
    __slots__ = ("id", "tenant_id", "name", "status", "description", "priority", "scope", "access", "rate_limits", "concurrency", "created_at", "updated_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    PRIORITY_FIELD_NUMBER: _ClassVar[int]
    SCOPE_FIELD_NUMBER: _ClassVar[int]
    ACCESS_FIELD_NUMBER: _ClassVar[int]
    RATE_LIMITS_FIELD_NUMBER: _ClassVar[int]
    CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    UPDATED_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: str
    name: str
    status: str
    description: str
    priority: int
    scope: InferenceAccessPolicyScope
    access: InferenceAccessPolicyAccess
    rate_limits: InferenceAccessPolicyRateLimits
    concurrency: InferenceAccessPolicyConcurrency
    created_at: _timestamp_pb2.Timestamp
    updated_at: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., name: _Optional[str] = ..., status: _Optional[str] = ..., description: _Optional[str] = ..., priority: _Optional[int] = ..., scope: _Optional[_Union[InferenceAccessPolicyScope, _Mapping]] = ..., access: _Optional[_Union[InferenceAccessPolicyAccess, _Mapping]] = ..., rate_limits: _Optional[_Union[InferenceAccessPolicyRateLimits, _Mapping]] = ..., concurrency: _Optional[_Union[InferenceAccessPolicyConcurrency, _Mapping]] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ..., updated_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class InferenceAccessPolicyScope(_message.Message):
    __slots__ = ("type", "inference_service_ids", "api_key_ids")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_IDS_FIELD_NUMBER: _ClassVar[int]
    API_KEY_IDS_FIELD_NUMBER: _ClassVar[int]
    type: str
    inference_service_ids: _containers.RepeatedScalarFieldContainer[str]
    api_key_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, type: _Optional[str] = ..., inference_service_ids: _Optional[_Iterable[str]] = ..., api_key_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class InferenceAccessPolicyAccess(_message.Message):
    __slots__ = ("allow_all_tenant_keys", "allow_api_key_ids", "deny_api_key_ids")
    ALLOW_ALL_TENANT_KEYS_FIELD_NUMBER: _ClassVar[int]
    ALLOW_API_KEY_IDS_FIELD_NUMBER: _ClassVar[int]
    DENY_API_KEY_IDS_FIELD_NUMBER: _ClassVar[int]
    allow_all_tenant_keys: bool
    allow_api_key_ids: _containers.RepeatedScalarFieldContainer[str]
    deny_api_key_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, allow_all_tenant_keys: _Optional[bool] = ..., allow_api_key_ids: _Optional[_Iterable[str]] = ..., deny_api_key_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class InferenceAccessPolicyRateLimits(_message.Message):
    __slots__ = ("qps", "rpm")
    QPS_FIELD_NUMBER: _ClassVar[int]
    RPM_FIELD_NUMBER: _ClassVar[int]
    qps: int
    rpm: int
    def __init__(self, qps: _Optional[int] = ..., rpm: _Optional[int] = ...) -> None: ...

class InferenceAccessPolicyConcurrency(_message.Message):
    __slots__ = ("max_in_flight", "lease_ttl_seconds")
    MAX_IN_FLIGHT_FIELD_NUMBER: _ClassVar[int]
    LEASE_TTL_SECONDS_FIELD_NUMBER: _ClassVar[int]
    max_in_flight: int
    lease_ttl_seconds: int
    def __init__(self, max_in_flight: _Optional[int] = ..., lease_ttl_seconds: _Optional[int] = ...) -> None: ...

class InferenceAccessPolicyBinding(_message.Message):
    __slots__ = ("policy_id", "inference_service_id", "priority")
    POLICY_ID_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    PRIORITY_FIELD_NUMBER: _ClassVar[int]
    policy_id: str
    inference_service_id: str
    priority: int
    def __init__(self, policy_id: _Optional[str] = ..., inference_service_id: _Optional[str] = ..., priority: _Optional[int] = ...) -> None: ...

class InferenceAccessPolicyEvent(_message.Message):
    __slots__ = ("id", "tenant_id", "policy_id", "inference_service_id", "api_key_id", "key_prefix", "request_id", "openai_path", "external_model", "decision", "reason_code", "http_status", "retry_after_seconds", "created_at")
    ID_FIELD_NUMBER: _ClassVar[int]
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    POLICY_ID_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    API_KEY_ID_FIELD_NUMBER: _ClassVar[int]
    KEY_PREFIX_FIELD_NUMBER: _ClassVar[int]
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    OPENAI_PATH_FIELD_NUMBER: _ClassVar[int]
    EXTERNAL_MODEL_FIELD_NUMBER: _ClassVar[int]
    DECISION_FIELD_NUMBER: _ClassVar[int]
    REASON_CODE_FIELD_NUMBER: _ClassVar[int]
    HTTP_STATUS_FIELD_NUMBER: _ClassVar[int]
    RETRY_AFTER_SECONDS_FIELD_NUMBER: _ClassVar[int]
    CREATED_AT_FIELD_NUMBER: _ClassVar[int]
    id: str
    tenant_id: str
    policy_id: str
    inference_service_id: str
    api_key_id: str
    key_prefix: str
    request_id: str
    openai_path: str
    external_model: str
    decision: str
    reason_code: str
    http_status: int
    retry_after_seconds: int
    created_at: _timestamp_pb2.Timestamp
    def __init__(self, id: _Optional[str] = ..., tenant_id: _Optional[str] = ..., policy_id: _Optional[str] = ..., inference_service_id: _Optional[str] = ..., api_key_id: _Optional[str] = ..., key_prefix: _Optional[str] = ..., request_id: _Optional[str] = ..., openai_path: _Optional[str] = ..., external_model: _Optional[str] = ..., decision: _Optional[str] = ..., reason_code: _Optional[str] = ..., http_status: _Optional[int] = ..., retry_after_seconds: _Optional[int] = ..., created_at: _Optional[_Union[datetime.datetime, _timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class ListInferenceAccessPoliciesRequest(_message.Message):
    __slots__ = ("tenant_id",)
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    def __init__(self, tenant_id: _Optional[str] = ...) -> None: ...

class ListInferenceAccessPoliciesResponse(_message.Message):
    __slots__ = ("items",)
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    items: _containers.RepeatedCompositeFieldContainer[InferenceAccessPolicy]
    def __init__(self, items: _Optional[_Iterable[_Union[InferenceAccessPolicy, _Mapping]]] = ...) -> None: ...

class CreateInferenceAccessPolicyRequest(_message.Message):
    __slots__ = ("tenant_id", "idempotency_key", "policy")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    POLICY_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    idempotency_key: str
    policy: InferenceAccessPolicy
    def __init__(self, tenant_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., policy: _Optional[_Union[InferenceAccessPolicy, _Mapping]] = ...) -> None: ...

class GetInferenceAccessPolicyRequest(_message.Message):
    __slots__ = ("tenant_id", "policy_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    POLICY_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    policy_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., policy_id: _Optional[str] = ...) -> None: ...

class PatchInferenceAccessPolicyRequest(_message.Message):
    __slots__ = ("tenant_id", "policy_id", "idempotency_key", "policy", "request_hash")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    POLICY_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    POLICY_FIELD_NUMBER: _ClassVar[int]
    REQUEST_HASH_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    policy_id: str
    idempotency_key: str
    policy: InferenceAccessPolicy
    request_hash: str
    def __init__(self, tenant_id: _Optional[str] = ..., policy_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., policy: _Optional[_Union[InferenceAccessPolicy, _Mapping]] = ..., request_hash: _Optional[str] = ...) -> None: ...

class DeleteInferenceAccessPolicyRequest(_message.Message):
    __slots__ = ("tenant_id", "policy_id", "idempotency_key")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    POLICY_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    policy_id: str
    idempotency_key: str
    def __init__(self, tenant_id: _Optional[str] = ..., policy_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ...) -> None: ...

class DeleteInferenceAccessPolicyResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ListInferenceServicePoliciesRequest(_message.Message):
    __slots__ = ("tenant_id", "inference_service_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    inference_service_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., inference_service_id: _Optional[str] = ...) -> None: ...

class InferenceServicePolicies(_message.Message):
    __slots__ = ("inference_service_id", "policies")
    INFERENCE_SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    POLICIES_FIELD_NUMBER: _ClassVar[int]
    inference_service_id: str
    policies: _containers.RepeatedCompositeFieldContainer[InferenceAccessPolicy]
    def __init__(self, inference_service_id: _Optional[str] = ..., policies: _Optional[_Iterable[_Union[InferenceAccessPolicy, _Mapping]]] = ...) -> None: ...

class UpdateInferenceServicePoliciesRequest(_message.Message):
    __slots__ = ("tenant_id", "inference_service_id", "idempotency_key", "policy_ids")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    IDEMPOTENCY_KEY_FIELD_NUMBER: _ClassVar[int]
    POLICY_IDS_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    inference_service_id: str
    idempotency_key: str
    policy_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, tenant_id: _Optional[str] = ..., inference_service_id: _Optional[str] = ..., idempotency_key: _Optional[str] = ..., policy_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class ListInferencePolicyEventsRequest(_message.Message):
    __slots__ = ("tenant_id", "inference_service_id", "policy_id", "api_key_id", "decision", "limit", "cursor")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    POLICY_ID_FIELD_NUMBER: _ClassVar[int]
    API_KEY_ID_FIELD_NUMBER: _ClassVar[int]
    DECISION_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    CURSOR_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    inference_service_id: str
    policy_id: str
    api_key_id: str
    decision: str
    limit: int
    cursor: str
    def __init__(self, tenant_id: _Optional[str] = ..., inference_service_id: _Optional[str] = ..., policy_id: _Optional[str] = ..., api_key_id: _Optional[str] = ..., decision: _Optional[str] = ..., limit: _Optional[int] = ..., cursor: _Optional[str] = ...) -> None: ...

class InferencePolicyEventListResponse(_message.Message):
    __slots__ = ("items", "next_cursor")
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    NEXT_CURSOR_FIELD_NUMBER: _ClassVar[int]
    items: _containers.RepeatedCompositeFieldContainer[InferenceAccessPolicyEvent]
    next_cursor: str
    def __init__(self, items: _Optional[_Iterable[_Union[InferenceAccessPolicyEvent, _Mapping]]] = ..., next_cursor: _Optional[str] = ...) -> None: ...

class CheckInferenceAccessRequest(_message.Message):
    __slots__ = ("tenant_id", "user_id", "api_key_id", "key_prefix", "served_model_name", "openai_path", "request_id", "stream")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    USER_ID_FIELD_NUMBER: _ClassVar[int]
    API_KEY_ID_FIELD_NUMBER: _ClassVar[int]
    KEY_PREFIX_FIELD_NUMBER: _ClassVar[int]
    SERVED_MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    OPENAI_PATH_FIELD_NUMBER: _ClassVar[int]
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    STREAM_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    user_id: str
    api_key_id: str
    key_prefix: str
    served_model_name: str
    openai_path: str
    request_id: str
    stream: bool
    def __init__(self, tenant_id: _Optional[str] = ..., user_id: _Optional[str] = ..., api_key_id: _Optional[str] = ..., key_prefix: _Optional[str] = ..., served_model_name: _Optional[str] = ..., openai_path: _Optional[str] = ..., request_id: _Optional[str] = ..., stream: _Optional[bool] = ...) -> None: ...

class CheckInferenceAccessResponse(_message.Message):
    __slots__ = ("decision", "http_status", "reason_code", "policy_id", "lease_id", "retry_after_seconds", "inference_service_id")
    DECISION_FIELD_NUMBER: _ClassVar[int]
    HTTP_STATUS_FIELD_NUMBER: _ClassVar[int]
    REASON_CODE_FIELD_NUMBER: _ClassVar[int]
    POLICY_ID_FIELD_NUMBER: _ClassVar[int]
    LEASE_ID_FIELD_NUMBER: _ClassVar[int]
    RETRY_AFTER_SECONDS_FIELD_NUMBER: _ClassVar[int]
    INFERENCE_SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    decision: str
    http_status: int
    reason_code: str
    policy_id: str
    lease_id: str
    retry_after_seconds: int
    inference_service_id: str
    def __init__(self, decision: _Optional[str] = ..., http_status: _Optional[int] = ..., reason_code: _Optional[str] = ..., policy_id: _Optional[str] = ..., lease_id: _Optional[str] = ..., retry_after_seconds: _Optional[int] = ..., inference_service_id: _Optional[str] = ...) -> None: ...

class ReleaseInferenceAccessLeaseRequest(_message.Message):
    __slots__ = ("tenant_id", "lease_id")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    LEASE_ID_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    lease_id: str
    def __init__(self, tenant_id: _Optional[str] = ..., lease_id: _Optional[str] = ...) -> None: ...

class ReleaseInferenceAccessLeaseResponse(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ResolveInferenceServiceEndpointRequest(_message.Message):
    __slots__ = ("tenant_id", "served_model_name")
    TENANT_ID_FIELD_NUMBER: _ClassVar[int]
    SERVED_MODEL_NAME_FIELD_NUMBER: _ClassVar[int]
    tenant_id: str
    served_model_name: str
    def __init__(self, tenant_id: _Optional[str] = ..., served_model_name: _Optional[str] = ...) -> None: ...

class ResolveInferenceServiceEndpointResponse(_message.Message):
    __slots__ = ("inference_service_id", "runtime_endpoint")
    INFERENCE_SERVICE_ID_FIELD_NUMBER: _ClassVar[int]
    RUNTIME_ENDPOINT_FIELD_NUMBER: _ClassVar[int]
    inference_service_id: str
    runtime_endpoint: str
    def __init__(self, inference_service_id: _Optional[str] = ..., runtime_endpoint: _Optional[str] = ...) -> None: ...
