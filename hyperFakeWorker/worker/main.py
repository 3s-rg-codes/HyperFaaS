#!/bin/python
from ._grpc import add_proto_definitions
add_proto_definitions()

import click

@click.group()
def main():
    pass

@main.command()
def server():
    from .server.server import serve

    serve()

@main.command()
def client():
    import grpc
    from ._grpc.controller.controller_pb2_grpc import ControllerStub
    from ._grpc.controller.controller_pb2 import StatusRequest
    channel = grpc.insecure_channel("localhost:50051")
    stub = ControllerStub(channel)

    status_future = stub.Status(StatusRequest(nodeID="0"))
    status_future.result()


if __name__ == "__main__":
    main()
