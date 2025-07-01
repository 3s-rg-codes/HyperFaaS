#!/bin/python
from pathlib import Path
import logging

import click

from .api import add_proto_definitions
from .config import WorkerConfig
from .log import setup_logger

add_proto_definitions()

@click.group()
@click.option('--address', default='', help='Worker address.')
@click.option('--database-type', default='http', help='Type of the database.')
@click.option('--runtime', default='docker', help='Container runtime type.')
@click.option('--timeout', default=20, type=int, help='Timeout in seconds before leafnode listeners are removed from status stream updates.')
@click.option('--auto-remove', is_flag=True, help='Auto remove containers.')
@click.option('--log-level', default='info', help='Log level (debug, info, warn, error)')
@click.option('--log-format', default='text', help='Log format (json or text)')
@click.option('--log-file', default=None, help='Log file path (defaults to stdout)')
@click.option('--containerized', is_flag=True, help='Use socket to connect to Docker.')
@click.option("-m", "--model", "model", multiple=True, default=[], type=click.Path(resolve_path=True, path_type=Path, dir_okay=False, exists=True))
@click.option('--update-buffer-size', default=None, type=int, help='Update buffer size.')  
@click.pass_context
def main(ctx, address, database_type, runtime, timeout, auto_remove, log_level, log_format, log_file, containerized, update_buffer_size, model):
    setup_logger(log_level, log_file)

    db_address = "localhost:8999"
    if containerized:
        db_address = "database:8999/"

    if update_buffer_size is None:
        # If maxsize is <= 0, the queue size is infinite.
        update_buffer_size = -1

    # Pass context to other commands
    ctx.obj = WorkerConfig(
        # General
        address=address or "[::]:50051",
        database_type=database_type or "http",
        timeout=timeout,  # not used

        # Runtime
        runtime=runtime,  # not used
        auto_remove=auto_remove,  # not used
        containerized=containerized,

        # Log
        log_level=log_level,
        log_format=log_format,  # not implemented
        log_file=log_file,

        update_buffer_size=update_buffer_size,

        # Extra parameters
        db_address=db_address,

        # Models
        models=model
    )

@main.command()
@click.pass_obj
def server(config: WorkerConfig):
    from .server.server import serve

    serve(config)

@main.command()
def client():
    
    from .log import logger
    import grpc
    from .api.controller.controller_pb2_grpc import ControllerStub
    from .api.controller.controller_pb2 import StateRequest, StateResponse
    from .api.common.common_pb2 import FunctionID, InstanceID, CallRequest, CallResponse
    channel = grpc.insecure_channel("localhost:50051")
    stub = ControllerStub(channel)

    logger.info("Testing grpc server...")

    state: StateResponse = stub.State(StateRequest(node_id="12345"))
    for function in state.functions:
        print(f"State of functions: {function.function_id}")
        print(f"Checking {len(function.idle)} idle function instances:")
        for func in function.idle:
            print(f"Checking instance {func.instance_id}: {func}")
            call_future = stub.Call(CallRequest(instance_id=InstanceID(id=func.instance_id), function_id=function.function_id))
            print(call_future)
        print(f"Checking {len(function.running)} running function instances:")
        for func in function.running:
            print(f"Checking instance {func.instance_id}: {func}")
            call_future = stub.Call(CallRequest(instance_id=InstanceID(id=func.instance_id), function_id=function.function_id))
            print(call_future)
    
if __name__ == "__main__":
    main()
