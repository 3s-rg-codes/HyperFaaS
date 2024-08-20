import contextvars
from urllib import request

from pkg.pyFunctionRuntimeInterface.pyFuntionRuntimeInterface import Function, new, Response, Request


def main():
    function = new(120)

    function.ready(handler)


def handler(ctx, req: Request) -> Response:
    response: Response = Response(
        data="HELLO WORLD!",
        id=req.id
    )
    return response
