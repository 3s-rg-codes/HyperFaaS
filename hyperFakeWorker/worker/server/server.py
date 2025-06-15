import grpc
import concurrent.futures as futures
import multiprocessing
import time

from ..log import logger

from .controller import ControllerServicer
from ..api.controller import controller_pb2_grpc
    
def serve():
    logger.info("Starting up hyperFake worker")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=multiprocessing.cpu_count()*2), maximum_concurrent_rpcs=multiprocessing.cpu_count()*4)
    logger.info("Registering services...")
    controller_servicer = ControllerServicer()
    controller_pb2_grpc.add_ControllerServicer_to_server(controller_servicer, server)
    server.add_insecure_port("[::]:50051")
    logger.info(f"Starting to listen on {'[::]:50051'}")
    server.start()
    try:
        while True:
            time.sleep(6)
            logger.info(f"Active Functions: {controller_servicer._fn_mngr.num_recently_active_functions}")
            logger.info(f"Scheduled Functions: {controller_servicer._fn_mngr.num_functions}")
    except KeyboardInterrupt:
        server.stop(6.0)
        server.wait_for_termination()
