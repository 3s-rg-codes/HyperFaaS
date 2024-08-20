import contextvars
import time
from urllib import request

from pkg.pyFunctionRuntimeInterface.pyFuntionRuntimeInterface import Function, new, Response, Request


def main():
    function = new(120)

    function.ready(handler)


def handler(ctx, req: Request) -> Response:
    resp: Response = Response(
        data=req.data,
        id=req.id
    )
    time.sleep(2)
    raise RuntimeError("crash")
    return resp
