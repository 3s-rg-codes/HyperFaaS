#!/bin/python
from .grpc import add_proto_definitions
add_proto_definitions()

import click

@click.group()
def main():
    pass

@main.command()
def server():
    from .grpc.server import serve

    serve()

@main.command()
def client():
    import grpc
    from .grpc.controller.controller_pb2_grpc import ControllerStub
    channel = grpc.insecure_channel("localhost:50051")
    stub = ControllerStub(channel)

    status_future = stub.Status()


if __name__ == "__main__":
    main()
