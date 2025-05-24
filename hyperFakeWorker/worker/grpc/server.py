import grpc
import concurrent.futures as futures

from ..log import logger

from controller import controller_pb2_grpc

class ControllerServicer(controller_pb2_grpc.ControllerServicer):

    def Start(self, request, context):
        return super().Start(request, context)
    
    def Call(self, request, context):
        return super().Call(request, context)
    
    def Stop(self, request, context):
        return super().Stop(request, context)
    
    def Status(self, request, context):
        return super().Status(request, context)
    
    def Metrics(self, request, context):
        return super().Metrics(request, context)
    
    def State(self, request, context):
        return super().State(request, context)
    
def serve():
    logger.info("Starting up hyperFake worker")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    logger.info("Registering services...")
    controller_pb2_grpc.add_ControllerServicer_to_server(ControllerServicer(), server)
    server.add_insecure_port("[::]:50051")
    logger.info(f"Starting to listen on {'[::]:50051'}")
    server.start()
    server.wait_for_termination()