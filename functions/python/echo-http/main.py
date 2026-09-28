from http.server import BaseHTTPRequestHandler

from hyperfaas_runtime import ready

is_cold = True


class EchoHandler(BaseHTTPRequestHandler):
    def do_POST(self):
        global is_cold
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        self.send_response(200)
        self.send_header("X-Cold", "1" if is_cold else "0")
        self.end_headers()
        is_cold = False
        self.wfile.write(body)

    def log_message(self, format, *args):
        return


if __name__ == "__main__":
    ready(EchoHandler)
