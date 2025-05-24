import grpc
import concurrent.futures as futures

from ..log import logger

from .controller import ControllerServicer
from .._grpc.controller import controller_pb2_grpc
    
def serve():
    logger.info("Starting up hyperFake worker")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    logger.info("Registering services...")
    controller_pb2_grpc.add_ControllerServicer_to_server(ControllerServicer(), server)
    server.add_insecure_port("[::]:50051")
    logger.info(f"Starting to listen on {'[::]:50051'}")
    server.start()
    server.wait_for_termination()