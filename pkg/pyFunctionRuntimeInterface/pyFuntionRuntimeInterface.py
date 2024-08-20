import contextvars
import logging
import os
from dataclasses import dataclass
from datetime import time
from typing import Optional, Callable

import grpc

from proto.function import function_pb2_grpc, function_pb2
import proto.function

@dataclass
class Request:
    data: str
    id: str


@dataclass
class Response:
    data: str
    id: str

Handler = Callable[[contextvars.ContextVar, Request], Response]

class Function:
    def __init__(self, timeout: int, address: str, id: str,
                 request: Optional[Request] = None,
                 response: Optional[Response] = None):
        self.timeout = timeout
        self.address = address
        self.id = id
        self.request = request
        self.response = response

    def ready(self, handler: Handler):

        with (grpc.insecure_channel(self.address) as channel):
            c = function_pb2_grpc.FunctionServiceStub(channel)
            first = True

            payload = function_pb2.Payload()

            ctx = contextvars.ContextVar('function')

            while True:
                try:
                    deadline = time.time() + self.timeout
                    p = c.Ready((ctx, ), timeout=self.timeout)
                    first = False
                    logging.debug(f"Received request: {p.data}")
                    self.request = Request(data=p.data, id=p.id)
                    self.response = handler(ctx, self.request)
                    logging.debug(f"Function handler called and generated response: {self.response.data}")




def new(timeout: int) -> Function:
    address = os.getenv("CALLER_SERVER_ADDRESS")
    if address is None:
        address = ""
    return Function(timeout=timeout, address="{}:50052".format(address), id=getID())


def getID():
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
