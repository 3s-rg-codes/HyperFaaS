import grpc

from . import worker_pb2 as worker__pb2


class SandboxServiceStub(object):
    def __init__(self, channel):
        self.SignalReady = channel.unary_unary(
            '/hyperfaas.SandboxService/SignalReady',
            request_serializer=worker__pb2.SignalReadyRequest.SerializeToString,
            response_deserializer=worker__pb2.SignalReadyResponse.FromString,
        )
