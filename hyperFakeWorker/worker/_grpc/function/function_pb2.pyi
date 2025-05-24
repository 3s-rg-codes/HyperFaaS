from common import common_pb2 as _common_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Payload(_message.Message):
    __slots__ = ("data", "instanceId", "functionId", "firstExecution", "error")
    DATA_FIELD_NUMBER: _ClassVar[int]
    INSTANCEID_FIELD_NUMBER: _ClassVar[int]
    FUNCTIONID_FIELD_NUMBER: _ClassVar[int]
    FIRSTEXECUTION_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    data: bytes
    instanceId: _common_pb2.InstanceID
    functionId: _common_pb2.FunctionID
    firstExecution: bool
    error: _common_pb2.Error
    def __init__(self, data: _Optional[bytes] = ..., instanceId: _Optional[_Union[_common_pb2.InstanceID, _Mapping]] = ..., functionId: _Optional[_Union[_common_pb2.FunctionID, _Mapping]] = ..., firstExecution: bool = ..., error: _Optional[_Union[_common_pb2.Error, _Mapping]] = ...) -> None: ...

class Call(_message.Message):
    __slots__ = ("data", "instanceId", "functionId")
    DATA_FIELD_NUMBER: _ClassVar[int]
    INSTANCEID_FIELD_NUMBER: _ClassVar[int]
    FUNCTIONID_FIELD_NUMBER: _ClassVar[int]
    data: bytes
    instanceId: _common_pb2.InstanceID
    functionId: _common_pb2.FunctionID
    def __init__(self, data: _Optional[bytes] = ..., instanceId: _Optional[_Union[_common_pb2.InstanceID, _Mapping]] = ..., functionId: _Optional[_Union[_common_pb2.FunctionID, _Mapping]] = ...) -> None: ...
