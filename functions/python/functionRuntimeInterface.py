import contextvars
import logging
import os
from dataclasses import dataclass
from typing import Callable

import grpc

from proto.function import function_pb2_grpc, function_pb2


@dataclass
class Response:
    data: str
    id: str


@dataclass
class Request:
    data: str
    id: str


Handler = Callable[[contextvars.ContextVar, Request], Response]


def function_runtime_interface(handler: Handler):
    address = os.getenv("CALLER_SERVER_ADDRESS")
    if address is None:
        address = ""
        address = "{}:50052".format(address)
    ide = get_id()

    with (grpc.insecure_channel(address) as channel):
        c = function_pb2_grpc.FunctionServiceStub(channel)

        ctx = contextvars.ContextVar('func')
        firstexec = True
        while True:
            if firstexec:
                p = c.Ready()
            logging.debug(f"Received request: {p.data}")
            resp = handler(ctx, Request(id=p.id, data=p.data))
            logging.debug(f"Function handler called and generated response: {resp.data}")
            payload = function_pb2.Payload(data=resp.data, id=resp.id, firstExecution=firstexec)
            firstexec = False
            p = c.Ready(payload)


def get_id():
    id = ""
    try:
        with open('.env', 'r') as file:
            for line in file:
                if line.startswith("CONTAINER_ID="):
                    id = line.strip().split("CONTAINER_ID=")[1]
                    break
    except Exception as e:
        raise e

    return id
