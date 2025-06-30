#!/bin/python
from .api import add_proto_definitions
add_proto_definitions()

from pathlib import Path

import click

from .log import logger

@click.group()
def main():
    pass

@main.command()
@click.option("-m", "--model", "model", multiple=True, default=[], type=click.Path(resolve_path=True, path_type=Path, dir_okay=False, exists=True))
def server(model: list[Path]):
    from .server.server import serve

    serve(model)

@main.command()
def client():
    
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
