from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Error(_message.Message):
    __slots__ = ("message",)
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    message: str
    def __init__(self, message: _Optional[str] = ...) -> None: ...

class InstanceID(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class FunctionID(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: str
    def __init__(self, id: _Optional[str] = ...) -> None: ...

class ImageTag(_message.Message):
    __slots__ = ("tag",)
    TAG_FIELD_NUMBER: _ClassVar[int]
    tag: str
    def __init__(self, tag: _Optional[str] = ...) -> None: ...

class CallRequest(_message.Message):
    __slots__ = ("request_id", "instance_id", "data", "function_id")
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_ID_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    FUNCTION_ID_FIELD_NUMBER: _ClassVar[int]
    request_id: int
    instance_id: InstanceID
    data: bytes
    function_id: FunctionID
    def __init__(self, request_id: _Optional[int] = ..., instance_id: _Optional[_Union[InstanceID, _Mapping]] = ..., data: _Optional[bytes] = ..., function_id: _Optional[_Union[FunctionID, _Mapping]] = ...) -> None: ...

class CallResponse(_message.Message):
    __slots__ = ("request_id", "data", "error", "instance_id")
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_ID_FIELD_NUMBER: _ClassVar[int]
    request_id: int
    data: bytes
    error: Error
    instance_id: InstanceID
    def __init__(self, request_id: _Optional[int] = ..., data: _Optional[bytes] = ..., error: _Optional[_Union[Error, _Mapping]] = ..., instance_id: _Optional[_Union[InstanceID, _Mapping]] = ...) -> None: ...

class Config(_message.Message):
    __slots__ = ("memory", "cpu", "timeout", "max_concurrency")
    MEMORY_FIELD_NUMBER: _ClassVar[int]
    CPU_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    MAX_CONCURRENCY_FIELD_NUMBER: _ClassVar[int]
    memory: int
    cpu: CPUConfig
    timeout: int
    max_concurrency: int
    def __init__(self, memory: _Optional[int] = ..., cpu: _Optional[_Union[CPUConfig, _Mapping]] = ..., timeout: _Optional[int] = ..., max_concurrency: _Optional[int] = ...) -> None: ...

class CPUConfig(_message.Message):
    __slots__ = ("period", "quota")
    PERIOD_FIELD_NUMBER: _ClassVar[int]
    QUOTA_FIELD_NUMBER: _ClassVar[int]
    period: int
    quota: int
    def __init__(self, period: _Optional[int] = ..., quota: _Optional[int] = ...) -> None: ...
