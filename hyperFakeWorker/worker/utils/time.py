from datetime import datetime

from google.protobuf import timestamp_pb2 as _timestamp_pb2

def get_timestamp() -> _timestamp_pb2.Timestamp:
    return _timestamp_pb2.Timestamp(seconds=datetime.now().timestamp())