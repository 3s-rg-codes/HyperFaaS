import contextvars
import logging
import os
from dataclasses import dataclass
import grpc

from proto.function import function_pb2_grpc, function_pb2


def main():
    address = os.getenv("CALLER_SERVER_ADDRESS")
    if address is None:
     address = ""
     address="{}:50052".format(address)
    ide = getID()


    with (grpc.insecure_channel(address) as channel):
        c = function_pb2_grpc.FunctionServiceStub(channel)

        ctx = contextvars.ContextVar('func')
        firstexec = True
        while True:
            if firstexec:
              p = c.Ready()
            logging.debug(f"Received request: {p.data}")
            resp = handler(Request(id=p.id, data=p.data),ctx)
            logging.debug(f"Function handler called and generated response: {resp.data}")
            payload = function_pb2.Payload(data=resp.data,id=resp.id, firstExecution=firstexec)
            firstexec = False
            p = c.Ready(payload)


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

@dataclass
class Response:
    data: str
    id: str

@dataclass
class Request:
    data:str
    id: str

def handler(request, context) -> Response:
    return Response(data="Hello World", id=request.id)