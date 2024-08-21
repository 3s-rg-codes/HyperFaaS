import time

from functions.python.functionRuntimeInterface import Request, Handler, Response, function_runtime_interface


def handler(context, req: Request) -> Response:
    time.sleep(20)
    resp: Response = Response(
        data="finished sleeping",
        id=req.id
    )
    return resp


def main():
    function_runtime_interface(handler)
