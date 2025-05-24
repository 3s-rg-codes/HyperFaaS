import grpc

from .._grpc.controller import controller_pb2_grpc
from .._grpc.common.common_pb2 import FunctionID, InstanceID, CallRequest, CallResponse
from .._grpc.controller.controller_pb2 import StatusRequest, StatusUpdate, MetricsRequest, MetricsUpdate, StateRequest, StateResponse

from ..utils.time import get_timestamp

class ControllerServicer(controller_pb2_grpc.ControllerServicer):

    def Start(self, request: FunctionID, context: InstanceID):
        return super().Start(request, context)
    
    def Call(self, request: CallRequest, context: CallResponse):
        return super().Call(request, context)
    
    def Stop(self, request: InstanceID, context: InstanceID):
        return super().Stop(request, context)
    
    def Status(self, request: StatusRequest, context: grpc.ServicerContext):
        # Collect functions...
        functions = [None]
        for func in functions:
            response = StatusUpdate(
                instance_id=None,
                type=None,
                event=None,
                status=None,
                function_id=None,
                timestamp=get_timestamp(),   
            )
            yield response
    
    def Metrics(self, request: MetricsRequest, context: MetricsUpdate):
        return super().Metrics(request, context)
    
    def State(self, request: StateRequest, context: StateResponse):
        return super().State(request, context)