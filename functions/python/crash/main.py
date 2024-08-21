import time

from functions.python.functionRuntimeInterface import Request, Handler, Response, function_runtime_interface


def handler(context, req: Request) -> Response:
    resp: Response = Response(
        data="",
        id=req.id
    )
    time.sleep(2)
    raise RuntimeError("crash")
    return resp


def main():
    function_runtime_interface(handler)
