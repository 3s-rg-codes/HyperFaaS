from __future__ import annotations

import os
from http.server import BaseHTTPRequestHandler, HTTPServer
from concurrent.futures import ThreadPoolExecutor

import grpc

from . import worker_pb2, worker_pb2_grpc

DEFAULT_FUNCTION_PORT = "50052"


def _required_env(name: str) -> str:
    value = os.environ.get(name)
    if not value:
        raise RuntimeError(f"hyperfaas runtime: {name} is required")
    return value


def _signal_ready(controller_address: str, instance_id: int) -> None:
    with grpc.insecure_channel(controller_address) as channel:
        client = worker_pb2_grpc.SandboxServiceStub(channel)
        client.SignalReady(worker_pb2.SignalReadyRequest(instance_id=instance_id))


class ThreadPoolHTTPServer(HTTPServer):
    def __init__(self, server_address, RequestHandlerClass):
        super().__init__(server_address, RequestHandlerClass)
        self.executor = ThreadPoolExecutor(max_workers=256)

    def process_request(self, request, client_address):
        self.executor.submit(self.process_request_thread, request, client_address)

    def process_request_thread(self, request, client_address):
        try:
            self.finish_request(request, client_address)
        except Exception:
            self.handle_error(request, client_address)
        finally:
            self.shutdown_request(request)


def ready(handler: type[BaseHTTPRequestHandler]) -> None:
    controller_address = _required_env("CONTROLLER_ADDRESS")
    instance_id = int(_required_env("INSTANCE_ID"))
    function_port = int(os.environ.get("FUNCTION_PORT", DEFAULT_FUNCTION_PORT))

    server = ThreadPoolHTTPServer(("0.0.0.0", function_port), handler)
    _signal_ready(controller_address, instance_id)
    print(f"hyperfaas python runtime ready on {function_port}", flush=True)
    server.serve_forever()
