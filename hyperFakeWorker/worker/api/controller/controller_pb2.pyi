from common import common_pb2 as _common_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class VirtualizationType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TYPE_CONTAINER: _ClassVar[VirtualizationType]

class Event(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    EVENT_RESPONSE: _ClassVar[Event]
    EVENT_DOWN: _ClassVar[Event]
    EVENT_TIMEOUT: _ClassVar[Event]
    EVENT_START: _ClassVar[Event]
    EVENT_STOP: _ClassVar[Event]
    EVENT_CALL: _ClassVar[Event]
    EVENT_RUNNING: _ClassVar[Event]

class Status(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STATUS_SUCCESS: _ClassVar[Status]
    STATUS_FAILED: _ClassVar[Status]
TYPE_CONTAINER: VirtualizationType
EVENT_RESPONSE: Event
EVENT_DOWN: Event
EVENT_TIMEOUT: Event
EVENT_START: Event
EVENT_STOP: Event
EVENT_CALL: Event
EVENT_RUNNING: Event
STATUS_SUCCESS: Status
STATUS_FAILED: Status

class StatusUpdate(_message.Message):
    __slots__ = ("instance_id", "type", "event", "status", "function_id", "timestamp")
    INSTANCE_ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    FUNCTION_ID_FIELD_NUMBER: _ClassVar[int]
    TIMESTAMP_FIELD_NUMBER: _ClassVar[int]
    instance_id: _common_pb2.InstanceID
    type: VirtualizationType
    event: Event
    status: Status
    function_id: _common_pb2.FunctionID
    timestamp: _timestamp_pb2.Timestamp
    def __init__(self, instance_id: _Optional[_Union[_common_pb2.InstanceID, _Mapping]] = ..., type: _Optional[_Union[VirtualizationType, str]] = ..., event: _Optional[_Union[Event, str]] = ..., status: _Optional[_Union[Status, str]] = ..., function_id: _Optional[_Union[_common_pb2.FunctionID, _Mapping]] = ..., timestamp: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class StatusRequest(_message.Message):
    __slots__ = ("nodeID",)
    NODEID_FIELD_NUMBER: _ClassVar[int]
    nodeID: str
    def __init__(self, nodeID: _Optional[str] = ...) -> None: ...

class MetricsRequest(_message.Message):
    __slots__ = ("nodeID",)
    NODEID_FIELD_NUMBER: _ClassVar[int]
    nodeID: str
    def __init__(self, nodeID: _Optional[str] = ...) -> None: ...

class MetricsUpdate(_message.Message):
    __slots__ = ("used_ram_percent", "cpu_percent_percpu")
    USED_RAM_PERCENT_FIELD_NUMBER: _ClassVar[int]
    CPU_PERCENT_PERCPU_FIELD_NUMBER: _ClassVar[int]
    used_ram_percent: float
    cpu_percent_percpu: _containers.RepeatedScalarFieldContainer[float]
    def __init__(self, used_ram_percent: _Optional[float] = ..., cpu_percent_percpu: _Optional[_Iterable[float]] = ...) -> None: ...

class InstanceStateRequest(_message.Message):
    __slots__ = ("function_id", "instance_id", "event")
    FUNCTION_ID_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_ID_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    function_id: _common_pb2.FunctionID
    instance_id: _common_pb2.InstanceID
    event: Event
    def __init__(self, function_id: _Optional[_Union[_common_pb2.FunctionID, _Mapping]] = ..., instance_id: _Optional[_Union[_common_pb2.InstanceID, _Mapping]] = ..., event: _Optional[_Union[Event, str]] = ...) -> None: ...

class InstanceState(_message.Message):
    __slots__ = ("instance_id", "is_active", "time_since_last_work", "uptime")
    INSTANCE_ID_FIELD_NUMBER: _ClassVar[int]
    IS_ACTIVE_FIELD_NUMBER: _ClassVar[int]
    TIME_SINCE_LAST_WORK_FIELD_NUMBER: _ClassVar[int]
    UPTIME_FIELD_NUMBER: _ClassVar[int]
    instance_id: str
    is_active: bool
    time_since_last_work: int
    uptime: int
    def __init__(self, instance_id: _Optional[str] = ..., is_active: bool = ..., time_since_last_work: _Optional[int] = ..., uptime: _Optional[int] = ...) -> None: ...

class FunctionState(_message.Message):
    __slots__ = ("function_id", "running", "idle")
    FUNCTION_ID_FIELD_NUMBER: _ClassVar[int]
    RUNNING_FIELD_NUMBER: _ClassVar[int]
    IDLE_FIELD_NUMBER: _ClassVar[int]
    function_id: _common_pb2.FunctionID
    running: _containers.RepeatedCompositeFieldContainer[InstanceState]
    idle: _containers.RepeatedCompositeFieldContainer[InstanceState]
    def __init__(self, function_id: _Optional[_Union[_common_pb2.FunctionID, _Mapping]] = ..., running: _Optional[_Iterable[_Union[InstanceState, _Mapping]]] = ..., idle: _Optional[_Iterable[_Union[InstanceState, _Mapping]]] = ...) -> None: ...

class InstanceStateResponse(_message.Message):
    __slots__ = ("functions",)
    FUNCTIONS_FIELD_NUMBER: _ClassVar[int]
    functions: _containers.RepeatedCompositeFieldContainer[FunctionState]
    def __init__(self, functions: _Optional[_Iterable[_Union[FunctionState, _Mapping]]] = ...) -> None: ...

class StartResponse(_message.Message):
    __slots__ = ("instance_id", "instance_ip", "instance_name")
    INSTANCE_ID_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_IP_FIELD_NUMBER: _ClassVar[int]
    INSTANCE_NAME_FIELD_NUMBER: _ClassVar[int]
    instance_id: _common_pb2.InstanceID
    instance_ip: str
    instance_name: str
    def __init__(self, instance_id: _Optional[_Union[_common_pb2.InstanceID, _Mapping]] = ..., instance_ip: _Optional[str] = ..., instance_name: _Optional[str] = ...) -> None: ...
