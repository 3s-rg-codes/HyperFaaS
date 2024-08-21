from functions.python.functionRuntimeInterface import Request, Handler, Response, function_runtime_interface


def handler(context, req: Request) -> Response:
    return Response(data="Hello World", id=req.id)


def main():
    function_runtime_interface(handler)
