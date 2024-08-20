import contextvars
import time
from urllib import request

from pkg.pyFunctionRuntimeInterface.pyFuntionRuntimeInterface import Function, new, Response, Request


def main():
    function = new(120)

    function.ready(handler)


def handler(ctx, req: Request) -> Response:
    time.sleep(20)
    resp: Response = Response(
        data="finished sleeping",
        id=req.id
    )
    return resp
